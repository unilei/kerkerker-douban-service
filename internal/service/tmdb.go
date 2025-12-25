package service

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// TMDBService handles TMDB API interactions with key rotation
type TMDBService struct {
	apiKeys    []string
	baseURL    string
	imageBase  string
	httpClient *http.Client
	keyIndex   uint64 // 原子计数器，用于轮询
}

// NewTMDBService creates a new TMDBService with multiple API keys
func NewTMDBService(apiKeys []string, baseURL, imageBase string) *TMDBService {
	if len(apiKeys) > 0 {
		log.Info().Int("count", len(apiKeys)).Msg("🔑 TMDB API Keys 已配置，启用轮询模式")
	}
	return &TMDBService{
		apiKeys:   apiKeys,
		baseURL:   baseURL,
		imageBase: imageBase,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		keyIndex: 0,
	}
}

// getNextKey returns the next API key using round-robin
func (s *TMDBService) getNextKey() string {
	if len(s.apiKeys) == 0 {
		return ""
	}
	idx := atomic.AddUint64(&s.keyIndex, 1) - 1
	return s.apiKeys[idx%uint64(len(s.apiKeys))]
}

// TMDBSearchResult represents a TMDB search result
type TMDBSearchResult struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	BackdropPath  string  `json:"backdrop_path"`
	ReleaseDate   string  `json:"release_date"`
	VoteAverage   float64 `json:"vote_average"`
	Popularity    float64 `json:"popularity"`
}

// TMDBSearchResponse is the TMDB search API response
type TMDBSearchResponse struct {
	Results []TMDBSearchResult `json:"results"`
}

// SearchMovieBackdrop searches for a movie and returns its backdrop URL
func (s *TMDBService) SearchMovieBackdrop(title string, year string) (string, error) {
	apiKey := s.getNextKey()
	if apiKey == "" {
		return "", fmt.Errorf("TMDB API key not configured")
	}

	// Clean title - remove year in parentheses
	cleanTitle := title
	extractedYear := year

	if yearMatch := extractYearFromTitle(title); yearMatch != "" {
		extractedYear = yearMatch
		cleanTitle = removeYearFromTitle(title)
	}

	// Build search URL
	searchURL := fmt.Sprintf("%s/search/movie?query=%s&language=zh-CN",
		s.baseURL, url.QueryEscape(cleanTitle))
	if extractedYear != "" {
		searchURL += fmt.Sprintf("&year=%s", extractedYear)
	}

	// Make request
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("TMDB search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("TMDB returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result TMDBSearchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("failed to parse TMDB response: %w", err)
	}

	if len(result.Results) == 0 {
		log.Debug().Str("title", title).Msg("TMDB: no results found")
		return "", nil
	}

	// Find best match using scoring
	bestMatch := s.findBestMatch(result.Results, cleanTitle, extractedYear)
	if bestMatch == nil {
		return "", nil
	}

	log.Debug().
		Str("title", title).
		Str("matched", bestMatch.Title).
		Msg("TMDB: matched")

	return fmt.Sprintf("%s%s", s.imageBase, bestMatch.BackdropPath), nil
}

// findBestMatch finds the best matching result using a scoring algorithm
func (s *TMDBService) findBestMatch(results []TMDBSearchResult, searchTitle, year string) *TMDBSearchResult {
	var bestMatch *TMDBSearchResult
	bestScore := 0.0

	for i := range results {
		result := &results[i]
		if result.BackdropPath == "" {
			continue
		}

		score := 0.0

		// Year matching (most important)
		if year != "" && len(result.ReleaseDate) >= 4 {
			movieYear := result.ReleaseDate[:4]
			if movieYear == year {
				score += 100
			} else {
				yearDiff := abs(atoi(movieYear) - atoi(year))
				if yearDiff <= 1 {
					score += 50
				}
			}
		}

		// Title matching
		movieTitle := strings.ToLower(result.Title)
		if result.OriginalTitle != "" {
			movieTitle = strings.ToLower(result.OriginalTitle)
		}
		searchLower := strings.ToLower(searchTitle)

		if movieTitle == searchLower {
			score += 50
		} else if strings.Contains(movieTitle, searchLower) || strings.Contains(searchLower, movieTitle) {
			score += 25
		}

		// Popularity and rating
		score += result.VoteAverage * 2
		score += math.Log10(result.Popularity+1) * 5

		if score > bestScore {
			bestScore = score
			bestMatch = result
		}
	}

	return bestMatch
}

// IsConfigured returns true if TMDB is configured
func (s *TMDBService) IsConfigured() bool {
	return len(s.apiKeys) > 0
}

// KeyCount returns the number of configured API keys
func (s *TMDBService) KeyCount() int {
	return len(s.apiKeys)
}

// Helper functions
func extractYearFromTitle(title string) string {
	for i := len(title) - 1; i >= 4; i-- {
		if title[i] == ')' {
			start := i - 5
			if start >= 0 && title[start] == '(' {
				year := title[start+1 : i]
				if len(year) == 4 && isDigits(year) {
					return year
				}
			}
		}
	}
	return ""
}

func removeYearFromTitle(title string) string {
	for i := len(title) - 1; i >= 4; i-- {
		if title[i] == ')' {
			start := i - 5
			if start >= 0 && title[start] == '(' {
				year := title[start+1 : i]
				if len(year) == 4 && isDigits(year) {
					return strings.TrimSpace(title[:start])
				}
			}
		}
	}
	return title
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func atoi(s string) int {
	result := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			result = result*10 + int(c-'0')
		}
	}
	return result
}
