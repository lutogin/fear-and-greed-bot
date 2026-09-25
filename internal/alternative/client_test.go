package alternative

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

// A real response of the API, recorded on 2026-09-25.
func TestFetchRealResponse(t *testing.T) {
	body, err := os.ReadFile("../../testdata/alternative_fng_2026-09-25.json")
	if err != nil {
		t.Fatal(err)
	}
	r, req, err := fetch(t, http.StatusOK, string(body))
	if err != nil {
		t.Fatal(err)
	}
	want := fng.Reading{Value: 71, Zone: fng.Greed, Time: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)}
	if r != want {
		t.Errorf("reading = %+v, want %+v", r, want)
	}
	if req.Method != http.MethodGet || req.URL.Path != "/fng/" || req.URL.RawQuery != "limit=1" {
		t.Errorf("request = %s %s, want GET /fng/?limit=1", req.Method, req.URL)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
}

func TestFetchDocumentedExample(t *testing.T) {
	// The example from https://alternative.me/crypto/fear-and-greed-index/#api:
	// the newest value comes first.
	const body = `{
		"name": "Fear and Greed Index",
		"data": [
			{"value": "40", "value_classification": "Fear", "timestamp": "1551157200", "time_until_update": "68499"},
			{"value": "47", "value_classification": "Neutral", "timestamp": "1551070800"}
		],
		"metadata": {"error": null}
	}`
	r, _, err := fetch(t, http.StatusOK, body)
	if err != nil {
		t.Fatal(err)
	}
	want := fng.Reading{Value: 40, Zone: fng.Fear, Time: time.Unix(1551157200, 0).UTC()}
	if r != want {
		t.Errorf("reading = %+v, want %+v", r, want)
	}
}

func TestFetchAcceptsPlainNumbers(t *testing.T) {
	r, _, err := fetch(t, http.StatusOK, `{"data":[{"value":12,"value_classification":"Extreme Fear","timestamp":1790294400}],"metadata":{"error":null}}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != 12 || r.Zone != fng.ExtremeFear || !r.Time.Equal(time.Unix(1790294400, 0)) {
		t.Errorf("reading = %+v", r)
	}
}

func TestFetchWithoutClassification(t *testing.T) {
	r, _, err := fetch(t, http.StatusOK, `{"data":[{"value":"55","timestamp":"1790294400"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != 55 || r.Zone != "" {
		t.Errorf("reading = %+v, want value 55 without a zone", r)
	}
}

func TestFetchErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"http error", http.StatusTooManyRequests, `{"error":"slow down"}`, "429"},
		{"api error", http.StatusOK, `{"data":[],"metadata":{"error":"Invalid limit"}}`, "Invalid limit"},
		{"no data", http.StatusOK, `{"name":"Fear and Greed Index","data":[],"metadata":{"error":null}}`, "no data"},
		{"not json", http.StatusOK, `<html>`, "decode response"},
		{"not a number", http.StatusOK, `{"data":[{"value":"","timestamp":"1790294400"}]}`, "decode response"},
		{"missing value", http.StatusOK, `{"data":[{"timestamp":"1790294400"}]}`, "invalid index value"},
		{"value out of range", http.StatusOK, `{"data":[{"value":"140","timestamp":"1790294400"}]}`, "outside of 0..100"},
		{"date instead of timestamp", http.StatusOK, `{"data":[{"value":"40","timestamp":"25-09-2026"}]}`, "decode response"},
		{"missing timestamp", http.StatusOK, `{"data":[{"value":"40"}]}`, "invalid timestamp"},
		{"zero timestamp", http.StatusOK, `{"data":[{"value":"40","timestamp":"0"}]}`, "invalid timestamp"},
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
	if p.Name != "alternative.me" || p.URL != PageURL {
		t.Errorf("provider = %+v", p)
	}
}

// TestLiveFetch queries the real API. It is skipped by default:
// FNG_LIVE_TEST=1 go test ./internal/alternative -run Live -v
func TestLiveFetch(t *testing.T) {
	if os.Getenv("FNG_LIVE_TEST") == "" {
		t.Skip("set FNG_LIVE_TEST=1 to query api.alternative.me")
	}
	r, err := NewClient(&http.Client{Timeout: 30 * time.Second}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Fear & Greed Index: %v (%s) as of %s", r.Value, r.Zone, r.Time)
	if age := time.Since(r.Time); age > 3*24*time.Hour {
		t.Errorf("the latest value is %s old", age)
	}
}
