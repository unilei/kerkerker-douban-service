package service

import (
	"encoding/json"
	"fmt"
	"net/url"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/pkg/httpclient"

	"github.com/rs/zerolog/log"
)

// DoubanService handles Douban API interactions
type DoubanService struct {
	client *httpclient.Client
}

// NewDoubanService creates a new DoubanService
func NewDoubanService(client *httpclient.Client) *DoubanService {
	return &DoubanService{
		client: client,
	}
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

// ProxyCount returns the number of configured proxies
func (s *DoubanService) ProxyCount() int {
	return s.client.ProxyCount()
}
