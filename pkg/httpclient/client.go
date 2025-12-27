package httpclient

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Random User-Agent pool - Updated for 2024-2025
var userAgents = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
}

// Client is an HTTP client with retry and proxy support
type Client struct {
	httpClient *http.Client
	proxies    []string
	timeout    time.Duration
	retries    int
	retryDelay time.Duration
}

// NewClient creates a new HTTP client
func NewClient(proxies []string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		proxies:    proxies,
		timeout:    10 * time.Second,
		retries:    3,
		retryDelay: 1 * time.Second,
	}
}

// getRandomUserAgent returns a random user agent string
func getRandomUserAgent() string {
	return userAgents[rand.Intn(len(userAgents))]
}

// getRandomProxy returns a random proxy URL or empty string if none available
func (c *Client) getRandomProxy() string {
	if len(c.proxies) == 0 {
		return ""
	}
	return c.proxies[rand.Intn(len(c.proxies))]
}

// convertToProxyURL converts a Douban URL to a proxy URL
func (c *Client) convertToProxyURL(originalURL string) (string, bool) {
	proxy := c.getRandomProxy()
	if proxy == "" {
		return originalURL, false
	}

	parsed, err := url.Parse(originalURL)
	if err != nil {
		return originalURL, false
	}

	if !strings.Contains(parsed.Hostname(), "douban.com") {
		return originalURL, false
	}

	// Use proxy with path + query
	proxyURL := fmt.Sprintf("%s%s", proxy, parsed.RequestURI())
	return proxyURL, true
}

// Fetch makes an HTTP GET request with retry and proxy support
func (c *Client) Fetch(targetURL string) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= c.retries; attempt++ {
		// Convert to proxy URL (may use different proxy each retry)
		finalURL, useProxy := c.convertToProxyURL(targetURL)

		req, err := http.NewRequest("GET", finalURL, nil)
		if err != nil {
			lastErr = err
			continue
		}

		// Set headers only when not using proxy (proxy handles headers)
		if !useProxy {
			req.Header.Set("User-Agent", getRandomUserAgent())
			req.Header.Set("Referer", "https://movie.douban.com/")
			req.Header.Set("Accept", "application/json, text/plain, */*")
			req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
			req.Header.Set("Connection", "keep-alive")
			req.Header.Set("Cache-Control", "no-cache")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			log.Warn().
				Int("attempt", attempt).
				Err(err).
				Str("url", targetURL).
				Msg("Request failed")

			if attempt < c.retries {
				waitTime := c.retryDelay * time.Duration(math.Pow(2, float64(attempt-1)))
				time.Sleep(waitTime)
			}
			continue
		}

		// Handle rate limiting
		if resp.StatusCode == 403 || resp.StatusCode == 429 {
			resp.Body.Close() // 立即关闭，避免泄漏
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
			log.Warn().
				Int("attempt", attempt).
				Int("status", resp.StatusCode).
				Str("url", targetURL).
				Msg("Request rate limited")

			if attempt < c.retries {
				waitTime := c.retryDelay * time.Duration(math.Pow(2, float64(attempt-1)))
				time.Sleep(waitTime)
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close() // 立即关闭，避免泄漏
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
			continue
		}

		// 读取并立即关闭 body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() // 立即关闭，不使用 defer

		if err != nil {
			lastErr = err
			continue
		}

		return body, nil
	}

	return nil, fmt.Errorf("all retries failed: %w", lastErr)
}

// FetchJSON is a convenience method for fetching JSON data
func (c *Client) FetchJSON(targetURL string) ([]byte, error) {
	return c.Fetch(targetURL)
}

// HasProxy returns true if proxies are configured
func (c *Client) HasProxy() bool {
	return len(c.proxies) > 0
}

// ProxyCount returns the number of configured proxies
func (c *Client) ProxyCount() int {
	return len(c.proxies)
}
