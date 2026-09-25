// Package alternative reads the Crypto Fear & Greed Index from the free
// alternative.me API: https://alternative.me/crypto/fear-and-greed-index/#api
//
// The API rules require crediting alternative.me next to the displayed data;
// the bot does it in every message through Provider.
package alternative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"fear-and-greed-bot/internal/fng"
)

const (
	// PageURL is the public page with the index.
	PageURL = "https://alternative.me/crypto/fear-and-greed-index/"
	// DefaultBaseURL is the API host.
	DefaultBaseURL = "https://api.alternative.me"

	userAgent = "fear-and-greed-bot/1.0"
	maxBody   = 1 << 20
)

// Client fetches the current index value.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a client for the real API.
func NewClient(httpClient *http.Client) *Client {
	return &Client{BaseURL: DefaultBaseURL, HTTP: httpClient}
}

// Provider credits alternative.me, as its API rules require.
func (c *Client) Provider() fng.Provider {
	return fng.Provider{Name: "alternative.me", URL: PageURL}
}

// response is the body of GET /fng/. Numbers come as strings ("40"), which
// json.Number accepts as well as plain numbers.
type response struct {
	Data []struct {
		Value          json.Number `json:"value"`
		Classification string      `json:"value_classification"`
		Timestamp      json.Number `json:"timestamp"` // unix seconds
	} `json:"data"`
	Metadata struct {
		Error json.RawMessage `json:"error"` // null on success
	} `json:"metadata"`
}

// Fetch returns the latest index value.
func (c *Client) Fetch(ctx context.Context) (fng.Reading, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/fng/?limit=1", nil)
	if err != nil {
		return fng.Reading{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

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
	if e := strings.TrimSpace(string(r.Metadata.Error)); e != "" && e != "null" && e != `""` {
		return fng.Reading{}, fmt.Errorf("alternative.me error: %s", e)
	}
	if len(r.Data) == 0 {
		return fng.Reading{}, errors.New("response contains no data")
	}
	latest := r.Data[0] // the API returns the newest value first
	value, err := latest.Value.Float64()
	if err != nil {
		return fng.Reading{}, fmt.Errorf("invalid index value %q", latest.Value)
	}
	if value < 0 || value > 100 {
		return fng.Reading{}, fmt.Errorf("index value %v is outside of 0..100", value)
	}
	ts, err := latest.Timestamp.Int64()
	if err != nil || ts <= 0 {
		return fng.Reading{}, fmt.Errorf("invalid timestamp %q", latest.Timestamp)
	}
	return fng.Reading{
		Value: value,
		Zone:  fng.Zone(latest.Classification),
		Time:  time.Unix(ts, 0).UTC(),
	}, nil
}
