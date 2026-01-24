package model

import "time"

// ================== 通用响应 ==================

// APIResponse is the standard API response format
type APIResponse struct {
	Code    int         `json:"code"`
	Data    interface{} `json:"data,omitempty"`
	Message string      `json:"message,omitempty"`
	Source  string      `json:"source,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ================== 豆瓣数据模型 ==================

// Subject represents a movie or TV show
type Subject struct {
	ID          string `json:"id" bson:"_id"`
	Title       string `json:"title" bson:"title"`
	Rate        string `json:"rate" bson:"rate"`
	Cover       string `json:"cover" bson:"cover"`
	URL         string `json:"url" bson:"url"`
	EpisodeInfo string `json:"episode_info,omitempty" bson:"episode_info,omitempty"`
}

// SubjectDetail contains detailed information about a subject
type SubjectDetail struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Rate            string    `json:"rate"`
	URL             string    `json:"url"`
	Cover           string    `json:"cover"`
	Types           []string  `json:"types"`
	ReleaseYear     string    `json:"release_year"`
	Directors       []string  `json:"directors"`
	Actors          []string  `json:"actors"`
	Duration        string    `json:"duration"`
	Region          string    `json:"region"`
	EpisodesCount   string    `json:"episodes_count"`
	ShortComment    *Comment  `json:"short_comment,omitempty"`
	Photos          []Photo   `json:"photos,omitempty"`
	Comments        []Comment `json:"comments,omitempty"`
	Recommendations []Subject `json:"recommendations,omitempty"`
}

// HeroMovie is a movie for the hero banner
type HeroMovie struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Rate             string   `json:"rate"`
	Cover            string   `json:"cover"`
	PosterHorizontal string   `json:"poster_horizontal"`
	PosterVertical   string   `json:"poster_vertical"`
	URL              string   `json:"url"`
	EpisodeInfo      string   `json:"episode_info,omitempty"`
	Genres           []string `json:"genres,omitempty"`
	Description      string   `json:"description,omitempty"`
}

// CategoryData holds data for a category
type CategoryData struct {
	Name string    `json:"name"`
	Data []Subject `json:"data"`
}

// Photo represents a photo from Douban
type Photo struct {
	ID    string `json:"id"`
	Image string `json:"image"`
	Thumb string `json:"thumb"`
}

// Comment represents a comment
type Comment struct {
	ID      string        `json:"id,omitempty"`
	Content string        `json:"content"`
	Author  CommentAuthor `json:"author"`
}

// CommentAuthor represents the author of a comment
type CommentAuthor struct {
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}

// Pagination holds pagination information
type Pagination struct {
	Page    int  `json:"page"`
	Limit   int  `json:"limit"`
	Total   int  `json:"total"`
	HasMore bool `json:"hasMore"`
}

// ================== 搜索相关 ==================

// SuggestItem represents a search suggestion item
type SuggestItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	SubTitle string `json:"sub_title,omitempty"`
	Img      string `json:"img"`
	URL      string `json:"url"`
	Type     string `json:"type"`
	Year     string `json:"year,omitempty"`
	Episode  string `json:"episode,omitempty"`
}

// SearchResult contains search results
type SearchResult struct {
	Suggest  []SuggestItem `json:"suggest"`
	Advanced []Subject     `json:"advanced"`
}

// ================== 豆瓣 API 响应 ==================

// DoubanSearchResponse is the response from Douban search API
type DoubanSearchResponse struct {
	Subjects []Subject `json:"subjects"`
}

// DoubanAbstractResponse is the response from Douban abstract API
type DoubanAbstractResponse struct {
	Subject *DoubanAbstractSubject `json:"subject"`
}

// DoubanAbstractSubject contains abstract subject details
type DoubanAbstractSubject struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Rate          string   `json:"rate"`
	URL           string   `json:"url"`
	Types         []string `json:"types"`
	ReleaseYear   string   `json:"release_year"`
	Directors     []string `json:"directors"`
	Actors        []string `json:"actors"`
	Duration      string   `json:"duration"`
	Region        string   `json:"region"`
	EpisodesCount string   `json:"episodes_count"`
	ShortComment  *struct {
		Content string `json:"content"`
		Author  string `json:"author"`
	} `json:"short_comment"`
}

// DoubanPhoto is a photo from Douban API
type DoubanPhoto struct {
	ID    string `json:"id"`
	Image string `json:"image"`
	Thumb string `json:"thumb"`
}

// DoubanPhotosResponse is the response from Douban photos API
type DoubanPhotosResponse struct {
	Photos []DoubanPhoto `json:"photos"`
}

// DoubanComment is a comment from Douban API
type DoubanComment struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Author  struct {
		Name string `json:"name"`
	} `json:"author"`
}

// DoubanCommentsResponse is the response from Douban comments API
type DoubanCommentsResponse struct {
	Comments []DoubanComment `json:"comments"`
}

// DoubanRecommendation is a recommendation from Douban API
type DoubanRecommendation struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Cover string `json:"cover"`
	Rate  string `json:"rate"`
}

// DoubanRecommendationsResponse is the response from Douban recommendations API
type DoubanRecommendationsResponse struct {
	Recommendations []DoubanRecommendation `json:"recommendations"`
}

// ================== 缓存相关 ==================

// CachedData wraps cached data with metadata
type CachedData struct {
	Data      interface{} `json:"data"`
	CachedAt  time.Time   `json:"cached_at"`
	ExpiresAt time.Time   `json:"expires_at"`
}

// ================== 日历相关 ==================

// CalendarEntry 日历条目 - 单集信息
type CalendarEntry struct {
	ShowID        int     `json:"show_id"`
	ShowName      string  `json:"show_name"`
	ShowNameCN    string  `json:"show_name_cn,omitempty"`
	SeasonNumber  int     `json:"season_number"`
	EpisodeNumber int     `json:"episode_number"`
	EpisodeName   string  `json:"episode_name"`
	AirDate       string  `json:"air_date"`
	Poster        string  `json:"poster"`
	Backdrop      string  `json:"backdrop,omitempty"`
	Overview      string  `json:"overview,omitempty"`
	VoteAverage   float64 `json:"vote_average"`
	DoubanID      string  `json:"douban_id,omitempty"`
	DoubanRating  string  `json:"douban_rating,omitempty"`
}

// CalendarDay 日历中的一天
type CalendarDay struct {
	Date    string          `json:"date"`
	Entries []CalendarEntry `json:"entries"`
}

// CalendarResponse 日历响应
type CalendarResponse struct {
	StartDate string        `json:"start_date"`
	EndDate   string        `json:"end_date"`
	Days      []CalendarDay `json:"days"`
	Total     int           `json:"total"`
}

// ================== TMDB TV 相关 ==================

// TMDBTVShow TMDB 电视剧信息
type TMDBTVShow struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	OriginalName     string   `json:"original_name"`
	Overview         string   `json:"overview"`
	PosterPath       string   `json:"poster_path"`
	BackdropPath     string   `json:"backdrop_path"`
	FirstAirDate     string   `json:"first_air_date"`
	VoteAverage      float64  `json:"vote_average"`
	Popularity       float64  `json:"popularity"`
	OriginCountry    []string `json:"origin_country"`
	OriginalLanguage string   `json:"original_language"`
}

// TMDBTVResponse TMDB TV 列表响应
type TMDBTVResponse struct {
	Page         int          `json:"page"`
	Results      []TMDBTVShow `json:"results"`
	TotalPages   int          `json:"total_pages"`
	TotalResults int          `json:"total_results"`
}

// TMDBEpisode TMDB 剧集信息
type TMDBEpisode struct {
	ID            int     `json:"id"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	EpisodeNumber int     `json:"episode_number"`
	SeasonNumber  int     `json:"season_number"`
	StillPath     string  `json:"still_path"`
	VoteAverage   float64 `json:"vote_average"`
}

// TMDBSeason TMDB 季度信息
type TMDBSeason struct {
	ID           int           `json:"id"`
	Name         string        `json:"name"`
	Overview     string        `json:"overview"`
	AirDate      string        `json:"air_date"`
	SeasonNumber int           `json:"season_number"`
	PosterPath   string        `json:"poster_path"`
	Episodes     []TMDBEpisode `json:"episodes"`
}

// TMDBTVDetails TMDB 剧集详情
type TMDBTVDetails struct {
	ID               int          `json:"id"`
	Name             string       `json:"name"`
	OriginalName     string       `json:"original_name"`
	Overview         string       `json:"overview"`
	PosterPath       string       `json:"poster_path"`
	BackdropPath     string       `json:"backdrop_path"`
	FirstAirDate     string       `json:"first_air_date"`
	LastAirDate      string       `json:"last_air_date"`
	VoteAverage      float64      `json:"vote_average"`
	NumberOfSeasons  int          `json:"number_of_seasons"`
	NumberOfEpisodes int          `json:"number_of_episodes"`
	Seasons          []TMDBSeason `json:"seasons"`
	InProduction     bool         `json:"in_production"`
	Status           string       `json:"status"`
}
