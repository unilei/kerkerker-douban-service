package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/pkg/httpclient"

	"github.com/rs/zerolog/log"
)

// DoubanService handles Douban API interactions
type DoubanService struct {
	client        *httpclient.Client
	imageSyncer   *ImageSyncer
	movieStore    repository.MovieStore    // 持久层；为 nil 时退化为纯抓取模式
	snapshotStore repository.SnapshotStore // 列表型快照的持久兜底；为 nil 时不启用
}

// NewDoubanService creates a new DoubanService
func NewDoubanService(client *httpclient.Client, imageSyncers ...*ImageSyncer) *DoubanService {
	service := &DoubanService{
		client: client,
	}
	if len(imageSyncers) > 0 {
		service.imageSyncer = imageSyncers[0]
	}
	return service
}

// SetMovieStore 注入持久层。可选调用；传入 nil 表示不启用 Mongo 持久化（旧行为）。
func (s *DoubanService) SetMovieStore(store repository.MovieStore) {
	if s == nil {
		return
	}
	s.movieStore = store
}

// MovieStore 返回注入的持久层（可能为 nil）。
func (s *DoubanService) MovieStore() repository.MovieStore {
	if s == nil {
		return nil
	}
	return s.movieStore
}

// SetSnapshotStore 注入列表快照持久层。可选调用；传入 nil 表示不启用。
func (s *DoubanService) SetSnapshotStore(store repository.SnapshotStore) {
	if s == nil {
		return
	}
	s.snapshotStore = store
}

// SnapshotStore 返回注入的快照持久层（可能为 nil）。
func (s *DoubanService) SnapshotStore() repository.SnapshotStore {
	if s == nil {
		return nil
	}
	return s.snapshotStore
}

// SearchSubjects searches for subjects by tag
func (s *DoubanService) SearchSubjects(subjectType, tag string, limit, start int) (*model.DoubanSearchResponse, error) {
	u, _ := url.Parse("https://movie.douban.com/j/search_subjects")
	q := u.Query()
	q.Set("type", subjectType)
	q.Set("tag", tag)
	q.Set("page_limit", fmt.Sprintf("%d", limit))
	q.Set("page_start", fmt.Sprintf("%d", start))
	u.RawQuery = q.Encode()

	data, err := s.client.Fetch(u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subjects: %w", err)
	}

	var result model.DoubanSearchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse subjects response: %w", err)
	}

	log.Debug().
		Str("tag", tag).
		Int("count", len(result.Subjects)).
		Msg("Fetched subjects")

	return &result, nil
}

// GetSubjectAbstract gets abstract details for a subject
func (s *DoubanService) GetSubjectAbstract(subjectID string) (*model.DoubanAbstractResponse, error) {
	u := fmt.Sprintf("https://movie.douban.com/j/subject_abstract?subject_id=%s", subjectID)

	data, err := s.client.Fetch(u)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subject abstract: %w", err)
	}

	var result model.DoubanAbstractResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse subject abstract: %w", err)
	}

	return &result, nil
}

// 豆瓣条目页的剧情简介：页面里有两个 property="v:summary" 的 span，
// 带 class="short" 的是截断版，另一个才是全文（点「展开」显示的内容）。
var summarySpanRe = regexp.MustCompile(`(?s)<span([^>]*?)property="v:summary"([^>]*?)>(.*?)</span>`)
var summaryTagRe = regexp.MustCompile(`<[^>]+>`)
var doubanSuffixRe = regexp.MustCompile(`\(?©豆瓣\)?\s*$`)
var blankLineRe = regexp.MustCompile(`\n{3,}`)

// htmlEntityReplacer 处理简介里常见的 HTML 实体
var htmlEntityReplacer = strings.NewReplacer(
	"&nbsp;", " ",
	"&amp;", "&",
	"&lt;", "<",
	"&gt;", ">",
	"&quot;", "\"",
	"&#39;", "'",
	"&ldquo;", "\u201c",
	"&rdquo;", "\u201d",
	"&hellip;", "\u2026",
	"&mdash;", "\u2014",
)

// extractSummary 从条目页 HTML 中提取剧情简介全文。
// 优先取不带 class="short" 的完整版；仅存在截断版时取最后一个（最完整）。
func extractSummary(html string) string {
	matches := summarySpanRe.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return ""
	}

	pick := ""
	for _, m := range matches {
		attrs := m[1] + m[2]
		if !strings.Contains(attrs, `class="short"`) {
			pick = m[3]
		}
	}
	if pick == "" {
		pick = matches[len(matches)-1][3]
	}
	return cleanSummary(pick)
}

// cleanSummary 清洗简介 HTML 片段：br 转换行、去标签、反转义实体、规整空白。
func cleanSummary(fragment string) string {
	text := strings.ReplaceAll(fragment, "<br>", "\n")
	text = strings.ReplaceAll(text, "<br/>", "\n")
	text = strings.ReplaceAll(text, "<br />", "\n")
	text = summaryTagRe.ReplaceAllString(text, "")
	text = htmlEntityReplacer.Replace(text)
	text = doubanSuffixRe.ReplaceAllString(text, "")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	text = strings.Join(lines, "\n")
	text = blankLineRe.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}

// rexxarIntroResponse 是 m.douban.com rexxar API 中我们关心的字段
type rexxarIntroResponse struct {
	Intro string `json:"intro"`
}

// getRexxarIntro 从豆瓣移动端 rexxar API 抓取剧情简介。
// 生产环境中 movie.douban.com 的 HTML 条目页对数据中心 IP 返回 302 验证页，
// 而 m.douban.com 的 rexxar API 宽松得多，可作为主路径；
// 电影/剧集分别走 /movie/:id 与 /tv/:id，先电影后剧集。
func (s *DoubanService) getRexxarIntro(subjectID string) string {
	const mobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1"

	for _, kind := range []string{"movie", "tv"} {
		u := fmt.Sprintf("https://m.douban.com/rexxar/api/v2/%s/%s", kind, subjectID)
		headers := map[string]string{
			"User-Agent": mobileUA,
			"Referer":    fmt.Sprintf("https://m.douban.com/%s/%s/", kind, subjectID),
		}

		data, err := s.client.FetchDirect(u, headers)
		if err != nil {
			continue
		}

		var result rexxarIntroResponse
		if err := json.Unmarshal(data, &result); err == nil && result.Intro != "" {
			return cleanSummary(result.Intro)
		}
	}
	return ""
}

// GetSubjectIntro 抓取剧情简介全文：主路径为 m.douban rexxar API，
// 失败时退回豆瓣条目页 HTML 的 v:summary（可能受 IP 风控限制）。
// 全部失败返回空字符串，调用方按无简介降级处理，不影响其余字段。
func (s *DoubanService) GetSubjectIntro(subjectID string) string {
	if intro := s.getRexxarIntro(subjectID); intro != "" {
		return intro
	}

	u := fmt.Sprintf("https://movie.douban.com/subject/%s/", subjectID)

	data, err := s.client.Fetch(u)
	if err != nil {
		log.Debug().Err(err).Str("id", subjectID).Msg("Failed to fetch subject page for intro")
		return ""
	}

	intro := extractSummary(string(data))
	if intro == "" {
		log.Debug().Str("id", subjectID).Msg("No summary found for subject")
	}
	return intro
}

// GetSubjectSuggest gets search suggestions
func (s *DoubanService) GetSubjectSuggest(query string) ([]model.SuggestItem, error) {
	u := fmt.Sprintf("https://movie.douban.com/j/subject_suggest?q=%s", url.QueryEscape(query))

	data, err := s.client.Fetch(u)
	if err != nil {
		log.Warn().Err(err).Str("query", query).Msg("Failed to fetch suggestions")
		return []model.SuggestItem{}, nil
	}

	var result []model.SuggestItem
	if err := json.Unmarshal(data, &result); err != nil {
		log.Warn().Err(err).Msg("Failed to parse suggestions")
		return []model.SuggestItem{}, nil
	}

	return result, nil
}

// GetPhotos gets photos for a subject
func (s *DoubanService) GetPhotos(subjectID string, count int, photoType string) ([]model.Photo, error) {
	u := fmt.Sprintf("https://movie.douban.com/j/subject/%s/photos?type=%s&start=0&count=%d",
		subjectID, photoType, count)

	data, err := s.client.Fetch(u)
	if err != nil {
		log.Warn().Err(err).Str("subjectID", subjectID).Msg("Failed to fetch photos")
		return []model.Photo{}, nil
	}

	var result model.DoubanPhotosResponse
	if err := json.Unmarshal(data, &result); err != nil {
		log.Warn().Err(err).Msg("Failed to parse photos")
		return []model.Photo{}, nil
	}

	photos := make([]model.Photo, len(result.Photos))
	for i, p := range result.Photos {
		photos[i] = model.Photo{
			ID:    p.ID,
			Image: p.Image,
			Thumb: p.Thumb,
		}
	}

	return photos, nil
}

// GetComments gets comments for a subject
func (s *DoubanService) GetComments(subjectID string, limit int) ([]model.Comment, error) {
	u := fmt.Sprintf("https://movie.douban.com/j/subject/%s/comments?start=0&limit=%d&sort=new_score&status=P",
		subjectID, limit)

	data, err := s.client.Fetch(u)
	if err != nil {
		log.Warn().Err(err).Str("subjectID", subjectID).Msg("Failed to fetch comments")
		return []model.Comment{}, nil
	}

	var result model.DoubanCommentsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		log.Warn().Err(err).Msg("Failed to parse comments")
		return []model.Comment{}, nil
	}

	comments := make([]model.Comment, len(result.Comments))
	for i, c := range result.Comments {
		comments[i] = model.Comment{
			ID:      c.ID,
			Content: c.Content,
			Author: model.CommentAuthor{
				Name: c.Author.Name,
			},
		}
	}

	return comments, nil
}

// GetRecommendations gets recommendations for a subject
func (s *DoubanService) GetRecommendations(subjectID string) ([]model.Subject, error) {
	u := fmt.Sprintf("https://movie.douban.com/j/subject/%s/recommendations", subjectID)

	data, err := s.client.Fetch(u)
	if err != nil {
		log.Warn().Err(err).Str("subjectID", subjectID).Msg("Failed to fetch recommendations")
		return []model.Subject{}, nil
	}

	var result model.DoubanRecommendationsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		log.Warn().Err(err).Msg("Failed to parse recommendations")
		return []model.Subject{}, nil
	}

	subjects := make([]model.Subject, len(result.Recommendations))
	for i, r := range result.Recommendations {
		subjects[i] = model.Subject{
			ID:    r.ID,
			Title: r.Title,
			Cover: r.Cover,
			Rate:  r.Rate,
		}
	}

	return subjects, nil
}

// AdvancedSearch performs advanced search
func (s *DoubanService) AdvancedSearch(tags, sort, genres, yearRange string, start, limit int) ([]model.Subject, error) {
	u, _ := url.Parse("https://movie.douban.com/j/new_search_subjects")
	q := u.Query()
	q.Set("tags", tags)
	q.Set("sort", sort)
	q.Set("range", "0,10")
	q.Set("start", fmt.Sprintf("%d", start))
	q.Set("limit", fmt.Sprintf("%d", limit))
	if genres != "" {
		q.Set("genres", genres)
	}
	if yearRange != "" {
		q.Set("year_range", yearRange)
	}
	u.RawQuery = q.Encode()

	data, err := s.client.Fetch(u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to advanced search: %w", err)
	}

	var result struct {
		Data []model.Subject `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse advanced search: %w", err)
	}

	return result.Data, nil
}

// GetSearchTags gets available search tags
func (s *DoubanService) GetSearchTags(subjectType string) ([]string, error) {
	u := fmt.Sprintf("https://movie.douban.com/j/search_tags?type=%s", subjectType)

	data, err := s.client.Fetch(u)
	if err != nil {
		return []string{}, nil
	}

	var result struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return []string{}, nil
	}

	return result.Tags, nil
}

// HasProxy returns true if proxies are configured
func (s *DoubanService) HasProxy() bool {
	return s.client.HasProxy()
}

// FetchDetail 抓取豆瓣并组装出一条影片的完整详情（抽象 + 封面 + 剧照 + 评论 + 推荐）。
// detail handler 与定时刷新任务共用此逻辑，确保两条路径产出一致的 SubjectDetail。
// 第二个返回值表示是否拿到抽象数据；为 false 时调用方应视为未找到。
func (s *DoubanService) FetchDetail(ctx context.Context, doubanID string) (model.SubjectDetail, bool) {
	detail, err := s.GetSubjectAbstract(doubanID)
	if err != nil || detail.Subject == nil {
		return model.SubjectDetail{}, false
	}

	title := detail.Subject.Title
	searchQuery := cleanTitleForSearch(title)

	var (
		cover           string
		description     string
		photos          []model.Photo
		comments        []model.Comment
		recommendations []model.Subject
	)

	var wg sync.WaitGroup
	wg.Add(5)

	go func() {
		defer wg.Done()
		if searchQuery != "" {
			if suggestions, err := s.GetSubjectSuggest(searchQuery); err == nil {
				for _, sug := range suggestions {
					if sug.ID == doubanID {
						cover = sug.Img
						break
					}
				}
			}
		}
	}()

	go func() {
		defer wg.Done()
		description = s.GetSubjectIntro(doubanID)
	}()

	go func() {
		defer wg.Done()
		photos, _ = s.GetPhotos(doubanID, 6, "S")
	}()

	go func() {
		defer wg.Done()
		comments, _ = s.GetComments(doubanID, 5)
	}()

	go func() {
		defer wg.Done()
		recommendations, _ = s.GetRecommendations(doubanID)
		if len(recommendations) > 6 {
			recommendations = recommendations[:6]
		}
	}()

	wg.Wait()

	var shortComment *model.Comment
	if detail.Subject.ShortComment != nil {
		shortComment = &model.Comment{
			Content: detail.Subject.ShortComment.Content,
			Author: model.CommentAuthor{
				Name: detail.Subject.ShortComment.Author,
			},
		}
	}

	return model.SubjectDetail{
		ID:              detail.Subject.ID,
		Title:           detail.Subject.Title,
		Rate:            detail.Subject.Rate,
		URL:             detail.Subject.URL,
		Cover:           cover,
		Types:           detail.Subject.Types,
		ReleaseYear:     detail.Subject.ReleaseYear,
		Directors:       detail.Subject.Directors,
		Actors:          detail.Subject.Actors,
		Duration:        detail.Subject.Duration,
		Region:          detail.Subject.Region,
		EpisodesCount:   detail.Subject.EpisodesCount,
		Description:     description,
		ShortComment:    shortComment,
		Photos:          photos,
		Comments:        comments,
		Recommendations: recommendations,
	}, true
}

// ProxyCount returns the number of configured proxies
func (s *DoubanService) ProxyCount() int {
	return s.client.ProxyCount()
}

// ImageSyncEnabled returns true when Douban images will be mirrored to R2.
func (s *DoubanService) ImageSyncEnabled() bool {
	return s != nil && s.imageSyncer != nil && s.imageSyncer.Enabled()
}

// SyncSubjectImages rewrites Subject image fields to R2 URLs when enabled.
func (s *DoubanService) SyncSubjectImages(ctx context.Context, subjects []model.Subject) []model.Subject {
	if !s.ImageSyncEnabled() {
		return subjects
	}
	return s.imageSyncer.SyncSubjectImages(ctx, subjects)
}

// SyncCategoryDataImages rewrites category data image fields to R2 URLs when enabled.
func (s *DoubanService) SyncCategoryDataImages(ctx context.Context, categories []model.CategoryData) []model.CategoryData {
	if !s.ImageSyncEnabled() {
		return categories
	}
	return s.imageSyncer.SyncCategoryDataImages(ctx, categories)
}

// SyncSearchResultImages rewrites search result image fields to R2 URLs when enabled.
func (s *DoubanService) SyncSearchResultImages(ctx context.Context, result model.SearchResult) model.SearchResult {
	if !s.ImageSyncEnabled() {
		return result
	}
	return s.imageSyncer.SyncSearchResultImages(ctx, result)
}

// SyncSubjectDetailImages rewrites detail image fields to R2 URLs when enabled.
func (s *DoubanService) SyncSubjectDetailImages(ctx context.Context, detail model.SubjectDetail) model.SubjectDetail {
	if !s.ImageSyncEnabled() {
		return detail
	}
	return s.imageSyncer.SyncSubjectDetailImages(ctx, detail)
}

// SyncHeroImages rewrites hero image fields to R2 URLs when enabled.
func (s *DoubanService) SyncHeroImages(ctx context.Context, heroes []model.HeroMovie) []model.HeroMovie {
	if !s.ImageSyncEnabled() {
		return heroes
	}
	return s.imageSyncer.SyncHeroImages(ctx, heroes)
}

// cleanTitleForSearch 从完整标题中提取用于搜索的简短标题（去除控制字符、年份、外文片段）。
// detail handler 与定时刷新任务共用此逻辑。
func cleanTitleForSearch(title string) string {
	re := regexp.MustCompile(`[\x{200B}-\x{200F}\x{2028}-\x{202F}\x{FEFF}]`)
	cleaned := re.ReplaceAllString(title, "")

	reYear := regexp.MustCompile(`\s*[\(（]\d{4}[\)）]\s*`)
	cleaned = reYear.ReplaceAllString(cleaned, "")

	parts := strings.Fields(cleaned)
	if len(parts) > 0 {
		return strings.TrimSpace(parts[0])
	}
	return strings.TrimSpace(cleaned)
}
