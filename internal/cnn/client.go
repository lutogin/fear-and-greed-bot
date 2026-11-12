// Package cnn reads the CNN Fear & Greed Index of the US stock market shown on
// https://edition.cnn.com/markets/fear-and-greed.
//
// CNN has no official API: the page loads the index from a JSON endpoint of its
// data visualisation service, and the client makes the same request. The
// endpoint answers non-browser clients with HTTP 418, so the client presents
// itself as a browser.
package cnn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"fear-and-greed-bot/internal/fng"
)

const (
	// PageURL is the public page with the index.
	PageURL = "https://edition.cnn.com/markets/fear-and-greed"
	// DefaultBaseURL is the host the page loads its data from.
	DefaultBaseURL = "https://production.dataviz.cnn.io"

	graphPath = "/index/fearandgreed/graphdata"
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	maxBody   = 16 << 20
)

// Client fetches the current index value.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a client for the real site.
func NewClient(httpClient *http.Client) *Client {
	return &Client{BaseURL: DefaultBaseURL, HTTP: httpClient}
}

// Provider credits CNN as the source of the data.
func (c *Client) Provider() fng.Provider {
	return fng.Provider{Name: "CNN", URL: PageURL}
}

// response is the part of the graphdata body the bot needs; the body also holds
// the index history and its seven components.
type response struct {
	FearAndGreed *struct {
		Score     *float64 `json:"score"`
		Rating    string   `json:"rating"`    // "extreme fear" .. "extreme greed"
		Timestamp string   `json:"timestamp"` // RFC 3339, e.g. "2026-11-12T21:43:31+00:00"
	} `json:"fear_and_greed"`
}

// Fetch returns the latest index value.
func (c *Client) Fetch(ctx context.Context) (fng.Reading, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+graphPath, nil)
	if err != nil {
		return fng.Reading{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", "https://edition.cnn.com")
	req.Header.Set("Referer", "https://edition.cnn.com/")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fng.Reading{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fng.Reading{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fng.Reading{}, fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return fng.Reading{}, fmt.Errorf("decode response: %w", err)
	}
	cur := r.FearAndGreed
	if cur == nil || cur.Score == nil {
		return fng.Reading{}, errors.New("response contains no index value")
	}
	// The page shows the score rounded to an integer; the bot reports and
	// compares the same number, so an alert matches what the user sees there.
	value := math.Round(*cur.Score)
	if value < 0 || value > 100 {
		return fng.Reading{}, fmt.Errorf("index value %v is outside of 0..100", *cur.Score)
	}
	ts, err := time.Parse(time.RFC3339, cur.Timestamp)
	if err != nil {
		return fng.Reading{}, fmt.Errorf("invalid timestamp %q", cur.Timestamp)
	}
	return fng.Reading{Value: value, Zone: zone(cur.Rating), Time: ts.UTC()}, nil
}

// zone converts CNN's lower-case rating to the shared zone names; an unknown
// rating is kept as is.
func zone(rating string) fng.Zone {
	for _, z := range []fng.Zone{fng.ExtremeFear, fng.Fear, fng.Neutral, fng.Greed, fng.ExtremeGreed} {
		if strings.EqualFold(strings.TrimSpace(rating), string(z)) {
			return z
		}
	}
	return fng.Zone(strings.TrimSpace(rating))
}
