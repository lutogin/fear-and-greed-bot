package cnn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"fear-and-greed-bot/internal/fng"
)

// fetch serves body with status from a fake API and returns the client result
// together with the request the client made.
func fetch(t *testing.T, status int, body string) (fng.Reading, *http.Request, error) {
	t.Helper()
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL + "/", HTTP: srv.Client()}
	r, err := c.Fetch(context.Background())
	return r, got, err
}

// A real response of the endpoint, recorded on 2026-11-12 with the score 39.94 (fear).
func TestFetchRealResponse(t *testing.T) {
	body, err := os.ReadFile("../../testdata/cnn_graphdata_2026-11-12.json")
	if err != nil {
		t.Fatal(err)
	}
	r, req, err := fetch(t, http.StatusOK, string(body))
	if err != nil {
		t.Fatal(err)
	}
	want := fng.Reading{Value: 40, Zone: fng.Fear, Time: time.Date(2026, 11, 12, 21, 43, 31, 0, time.UTC)}
	if r != want {
		t.Errorf("reading = %+v, want %+v", r, want)
	}
	if req.Method != http.MethodGet || req.URL.Path != graphPath || req.URL.RawQuery != "" {
		t.Errorf("request = %s %s, want GET %s", req.Method, req.URL, graphPath)
	}
	if ua := req.Header.Get("User-Agent"); !strings.HasPrefix(ua, "Mozilla/5.0") {
		t.Errorf("User-Agent = %q, want a browser one", ua)
	}
}

func TestFetchRatings(t *testing.T) {
	for rating, want := range map[string]fng.Zone{
		"extreme fear":  fng.ExtremeFear,
		"fear":          fng.Fear,
		"neutral":       fng.Neutral,
		"greed":         fng.Greed,
		"Extreme Greed": fng.ExtremeGreed,
		"euphoria":      "euphoria",
		"":              "",
	} {
		r, _, err := fetch(t, http.StatusOK, `{"fear_and_greed":{"score":50,"rating":"`+rating+`","timestamp":"2026-11-12T21:43:31+00:00"}}`)
		if err != nil {
			t.Fatal(err)
		}
		if r.Zone != want {
			t.Errorf("rating %q: zone = %q, want %q", rating, r.Zone, want)
		}
	}
}

func TestFetchRoundsScoreAsThePage(t *testing.T) {
	for score, want := range map[string]float64{"24.49": 24, "24.5": 25, "0.2": 0, "99.9": 100} {
		r, _, err := fetch(t, http.StatusOK, `{"fear_and_greed":{"score":`+score+`,"rating":"fear","timestamp":"2026-11-12T21:43:31.181000+00:00"}}`)
		if err != nil {
			t.Fatal(err)
		}
		if r.Value != want {
			t.Errorf("score %s: value = %v, want %v", score, r.Value, want)
		}
		if want := time.Date(2026, 11, 12, 21, 43, 31, 181000000, time.UTC); !r.Time.Equal(want) {
			t.Errorf("time = %s, want %s", r.Time, want)
		}
	}
}

func TestFetchErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"blocked client", http.StatusTeapot, `I'm a teapot. You're a bot.`, "418"},
		{"not json", http.StatusOK, `<html>`, "decode response"},
		{"no index", http.StatusOK, `{"market_volatility_vix":{"score":50}}`, "no index value"},
		{"no score", http.StatusOK, `{"fear_and_greed":{"rating":"fear","timestamp":"2026-11-12T21:43:31+00:00"}}`, "no index value"},
		{"score is a string", http.StatusOK, `{"fear_and_greed":{"score":"40"}}`, "decode response"},
		{"score out of range", http.StatusOK, `{"fear_and_greed":{"score":140,"timestamp":"2026-11-12T21:43:31+00:00"}}`, "outside of 0..100"},
		{"missing timestamp", http.StatusOK, `{"fear_and_greed":{"score":40}}`, "invalid timestamp"},
		{"unix timestamp", http.StatusOK, `{"fear_and_greed":{"score":40,"timestamp":"1794519811"}}`, "invalid timestamp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _, err := fetch(t, tt.status, tt.body)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Fetch() = %+v, %v; want error containing %q", r, err, tt.wantErr)
			}
		})
	}
}

func TestFetchHonoursContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request must not be sent with a cancelled context")
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Fetch(ctx); err == nil {
		t.Fatal("expected an error")
	}
}

func TestProvider(t *testing.T) {
	p := NewClient(http.DefaultClient).Provider()
	if p.Name != "CNN" || p.URL != PageURL {
		t.Errorf("provider = %+v", p)
	}
}

// TestLiveFetch queries the real endpoint. It is skipped by default:
// FNG_LIVE_TEST=1 go test ./internal/cnn -run Live -v
func TestLiveFetch(t *testing.T) {
	if os.Getenv("FNG_LIVE_TEST") == "" {
		t.Skip("set FNG_LIVE_TEST=1 to query production.dataviz.cnn.io")
	}
	r, err := NewClient(&http.Client{Timeout: 30 * time.Second}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CNN Fear & Greed Index: %v (%s) as of %s", r.Value, r.Zone, r.Time)
	// The index is updated on trading days; a long weekend leaves it a few days old.
	if age := time.Since(r.Time); age > 5*24*time.Hour {
		t.Errorf("the latest value is %s old", age)
	}
}
