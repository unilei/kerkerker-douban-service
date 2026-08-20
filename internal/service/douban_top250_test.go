package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFetchTop250UsesMobileCollectionEndpoint(t *testing.T) {
	var requestedURL string
	var requestedHeaders map[string]string
	fetch := func(rawURL string, headers map[string]string) ([]byte, error) {
		requestedURL = rawURL
		requestedHeaders = headers
		return top250CollectionJSON(t, nil), nil
	}

	subjects, err := fetchTop250(fetch)
	if err != nil {
		t.Fatalf("fetch Top 250: %v", err)
	}
	if requestedURL != top250CollectionURL {
		t.Fatalf("unexpected URL: %s", requestedURL)
	}
	if requestedHeaders["Referer"] != top250Referer || requestedHeaders["User-Agent"] != top250MobileUA {
		t.Fatalf("unexpected headers: %+v", requestedHeaders)
	}
	if len(subjects) != top250Total {
		t.Fatalf("expected %d subjects, got %d", top250Total, len(subjects))
	}
	if subjects[0].ID != "1000000" || subjects[249].ID != "1000249" {
		t.Fatalf("ranking order changed: first=%s last=%s", subjects[0].ID, subjects[249].ID)
	}
	if subjects[0].Rate != "9.9" {
		t.Fatalf("unexpected formatted rating: %s", subjects[0].Rate)
	}
}

func TestFetchTop250WrapsUpstreamFailure(t *testing.T) {
	_, err := fetchTop250(func(string, map[string]string) ([]byte, error) {
		return nil, errors.New("upstream unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "fetch top 250 collection") {
		t.Fatalf("expected wrapped upstream error, got %v", err)
	}
}

func TestParseTop250CollectionRejectsPartialPayload(t *testing.T) {
	count := top250Total - 1
	_, err := parseTop250Collection(top250CollectionJSON(t, func(response *top250CollectionResponse) {
		response.Items = response.Items[:count]
	}))
	if err == nil || !strings.Contains(err.Error(), "returned 249 subjects") {
		t.Fatalf("expected partial-payload error, got %v", err)
	}
}

func TestParseTop250CollectionRejectsDuplicateSubject(t *testing.T) {
	_, err := parseTop250Collection(top250CollectionJSON(t, func(response *top250CollectionResponse) {
		response.Items[1].ID = response.Items[0].ID
	}))
	if err == nil || !strings.Contains(err.Error(), "duplicate subject id") {
		t.Fatalf("expected duplicate-subject error, got %v", err)
	}
}

func TestParseTop250CollectionRejectsBrokenRankOrder(t *testing.T) {
	_, err := parseTop250Collection(top250CollectionJSON(t, func(response *top250CollectionResponse) {
		response.Items[1].Rank = 3
	}))
	if err == nil || !strings.Contains(err.Error(), "reported rank 3") {
		t.Fatalf("expected rank-order error, got %v", err)
	}
}

func top250CollectionJSON(t *testing.T, mutate func(*top250CollectionResponse)) []byte {
	t.Helper()
	response := top250CollectionResponse{
		Total: top250Total,
		Items: make([]top250CollectionItem, top250Total),
	}
	for index := range response.Items {
		id := fmt.Sprintf("%d", 1000000+index)
		response.Items[index] = top250CollectionItem{
			ID:       id,
			Title:    fmt.Sprintf("电影 %d", index+1),
			Rank:     index + 1,
			URL:      "https://movie.douban.com/subject/" + id + "/",
			CoverURL: "https://img.example.com/" + id + ".jpg",
		}
		response.Items[index].Rating.Value = 9.9 - float64(index%10)/10
	}
	if mutate != nil {
		mutate(&response)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return data
}
