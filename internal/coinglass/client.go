// Package coinglass reads the Crypto Fear & Greed Index shown on
// https://www.coinglass.com/pro/i/FearGreedIndex.
//
// The official CoinGlass API requires a paid key, and the page's HTML contains
// no value: the browser loads it from an internal endpoint that returns encrypted
// data. The client makes the same request as the page and decrypts the response
// the same way the page's JavaScript does.
package coinglass

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fear-and-greed-bot/internal/fng"
)

const (
	// PageURL is the public page with the index.
	PageURL = "https://www.coinglass.com/pro/i/FearGreedIndex"
	// DefaultBaseURL is the host the page loads its data from.
	DefaultBaseURL = "https://capi.coinglass.com"

	historyPath = "/api/index/history"
	userAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	maxBody     = 16 << 20
)

// Client fetches the current index value.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

// NewClient returns a client for the real site.
func NewClient(httpClient *http.Client) *Client {
	return &Client{BaseURL: DefaultBaseURL, HTTP: httpClient, Now: time.Now}
}

type envelope struct {
	Code json.RawMessage `json:"code"` // "0" on success
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// series is one chart of the page; the first one is the Fear & Greed Index.
type series struct {
	Dates  []float64  `json:"dates"` // unix milliseconds
	Prices []*float64 `json:"prices"`
	Values []*float64 `json:"values"`
}

// Fetch returns the latest index value.
func (c *Client) Fetch(ctx context.Context) (fng.Reading, error) {
	reqTS := strconv.FormatInt(c.Now().UnixMilli(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+historyPath+"?size=", nil)
	if err != nil {
		return fng.Reading{}, err
	}
	// The headers the page's JS sends; "encryption" and "cache-ts-v2" are required.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("language", "en")
	req.Header.Set("encryption", "true")
	req.Header.Set("cache-ts-v2", reqTS)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", "https://www.coinglass.com")
	req.Header.Set("Referer", "https://www.coinglass.com/")

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

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fng.Reading{}, fmt.Errorf("decode response: %w", err)
	}
	if code := string(bytes.Trim(env.Code, `"`)); code != "0" {
		return fng.Reading{}, fmt.Errorf("coinglass error: code=%s msg=%q", code, env.Msg)
	}
	payload, err := decodePayload(resp.Header, env.Data, reqTS)
	if err != nil {
		return fng.Reading{}, err
	}
	return parseReading(payload)
}

// decodePayload returns the JSON of the "data" field, decrypting it if the
// response says it is encrypted.
func decodePayload(h http.Header, data json.RawMessage, reqTS string) ([]byte, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, errors.New("response contains no data")
	}
	if h.Get("user") == "" || h.Get("encryption") == "" {
		return data, nil
	}
	var encrypted string
	if err := json.Unmarshal(data, &encrypted); err != nil {
		return nil, fmt.Errorf("encrypted data is not a string: %w", err)
	}
	key, err := sessionKey(h.Get("v"), reqTS, h.Get("time"), historyPath)
	if err != nil {
		return nil, err
	}
	dataKey, err := decrypt(h.Get("user"), key)
	if err != nil {
		return nil, fmt.Errorf("decrypt data key (v=%s): %w", h.Get("v"), err)
	}
	payload, err := decrypt(encrypted, dataKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt data (v=%s): %w", h.Get("v"), err)
	}
	return payload, nil
}

// parseReading returns the latest non-empty point of the Fear & Greed series.
func parseReading(payload []byte) (fng.Reading, error) {
	var charts []series
	if err := json.Unmarshal(payload, &charts); err != nil {
		return fng.Reading{}, fmt.Errorf("decode index history: %w", err)
	}
	if len(charts) == 0 {
		return fng.Reading{}, errors.New("index history is empty")
	}
	s := charts[0] // the page takes the index from the first chart as well
	for i := len(s.Values) - 1; i >= 0; i-- {
		v := s.Values[i]
		if v == nil {
			continue
		}
		if *v < 0 || *v > 100 {
			return fng.Reading{}, fmt.Errorf("index value %v is outside of 0..100", *v)
		}
		r := fng.Reading{Value: *v}
		if i < len(s.Dates) && s.Dates[i] > 0 {
			r.Time = time.UnixMilli(int64(s.Dates[i])).UTC()
		}
		if i < len(s.Prices) && s.Prices[i] != nil {
			r.Price = *s.Prices[i]
		}
		return r, nil
	}
	return fng.Reading{}, errors.New("index history has no values")
}
