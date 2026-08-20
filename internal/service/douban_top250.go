package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"kerkerker-douban-service/internal/model"
)

const (
	top250CollectionURL = "https://m.douban.com/rexxar/api/v2/subject_collection/movie_top250/items?start=0&count=250&items_only=1&for_mobile=1"
	top250Referer       = "https://m.douban.com/subject_collection/movie_top250/"
	top250MobileUA      = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1"
	top250Total         = 250
)

type top250Fetcher func(string, map[string]string) ([]byte, error)

type top250CollectionResponse struct {
	Total int                    `json:"total"`
	Items []top250CollectionItem `json:"subject_collection_items"`
}

type top250CollectionItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Rank     int    `json:"rank"`
	URL      string `json:"url"`
	CoverURL string `json:"cover_url"`
	Rating   struct {
		Value float64 `json:"value"`
	} `json:"rating"`
}

// GetTop250 fetches the complete Douban Top 250 ranking in a single request.
//
// The mobile subject-collection endpoint is the ranking source of truth. The
// response must contain exactly 250 valid, unique subjects in rank order;
// otherwise the method fails instead of returning and caching a partial or
// substituted list.
func (s *DoubanService) GetTop250() ([]model.Subject, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("douban service is not initialized")
	}
	return fetchTop250(s.client.FetchDirect)
}

func fetchTop250(fetch top250Fetcher) ([]model.Subject, error) {
	if fetch == nil {
		return nil, fmt.Errorf("top 250 fetcher is not initialized")
	}

	data, err := fetch(top250CollectionURL, map[string]string{
		"User-Agent": top250MobileUA,
		"Referer":    top250Referer,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch top 250 collection: %w", err)
	}
	return parseTop250Collection(data)
}

func parseTop250Collection(data []byte) ([]model.Subject, error) {
	var response top250CollectionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("parse top 250 collection: %w", err)
	}
	if response.Total != top250Total {
		return nil, fmt.Errorf("top 250 collection reports total %d, expected %d", response.Total, top250Total)
	}
	if len(response.Items) != top250Total {
		return nil, fmt.Errorf("top 250 collection returned %d subjects, expected %d", len(response.Items), top250Total)
	}

	subjects := make([]model.Subject, 0, top250Total)
	seen := make(map[string]struct{}, top250Total)
	for index, item := range response.Items {
		expectedRank := index + 1
		if item.Rank != expectedRank {
			return nil, fmt.Errorf("top 250 rank %d contains reported rank %d", expectedRank, item.Rank)
		}

		item.ID = strings.TrimSpace(item.ID)
		item.Title = strings.TrimSpace(item.Title)
		item.URL = strings.TrimSpace(item.URL)
		item.CoverURL = strings.TrimSpace(item.CoverURL)
		if item.ID == "" || item.Title == "" || item.URL == "" || item.CoverURL == "" || item.Rating.Value <= 0 {
			return nil, fmt.Errorf("top 250 rank %d is missing required fields", expectedRank)
		}
		if _, exists := seen[item.ID]; exists {
			return nil, fmt.Errorf("top 250 contains duplicate subject id %s", item.ID)
		}
		seen[item.ID] = struct{}{}

		subjects = append(subjects, model.Subject{
			ID:    item.ID,
			Title: item.Title,
			Rate:  strconv.FormatFloat(item.Rating.Value, 'f', 1, 64),
			Cover: item.CoverURL,
			URL:   item.URL,
		})
	}
	return subjects, nil
}
