package coinglass

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"fear-and-greed-bot/internal/fng"
)

// historyPayload mimics the decrypted response: the first chart is the index.
const historyPayload = `[
	{"dates":[1790036401000,1790122801000,1790209201000],"prices":[86429.5,86470.3,84400.7],"values":[79,70,72]},
	{"dates":[1790121600000,1790272202967],"prices":[86158.7,null],"values":[100.847,101.073]}
]`

var (
	testNow     = time.Date(2026, 9, 24, 17, 54, 31, 655_000_000, time.UTC)
	testTS      = strconv.FormatInt(testNow.UnixMilli(), 10)
	wantReading = fng.Reading{Value: 72, Time: time.Date(2026, 9, 24, 0, 20, 1, 0, time.UTC), Price: 84400.7}
)

const (
	testDataKey    = "0123456789abcdef"
	testTimeHeader = "1700000000000"
)

// fakeResponse is what the fake capi.coinglass.com returns.
type fakeResponse struct {
	status  int
	headers map[string]string
	body    string
}

// encryptedResponse builds a response the way capi.coinglass.com encrypts it
// for the given "v" header, computing the key independently of the client code.
func encryptedResponse(t *testing.T, version, payload string, quotedKey, gzip bool) fakeResponse {
	t.Helper()
	seed := map[string]string{
		"0":  testTS, // the cache-ts-v2 request header
		"1":  historyPath,
		"2":  testTimeHeader,
		"55": "170b070da9654622",
		"66": "d6537d845a964081",
		"77": "863f08689c97435b",
	}[version]
	sessionKey := base64.StdEncoding.EncodeToString([]byte(seed))[:16]
	user := testDataKey
	if quotedKey {
		user = `"` + user + `"`
	}
	body, err := json.Marshal(map[string]any{
		"code":    "0",
		"msg":     "success",
		"success": true,
		"data":    encrypt(t, []byte(payload), []byte(testDataKey), gzip),
	})
	if err != nil {
		t.Fatal(err)
	}
	return fakeResponse{
		headers: map[string]string{
			"encryption": "true",
			"v":          version,
			"time":       testTimeHeader,
			"user":       encrypt(t, []byte(user), []byte(sessionKey), false),
		},
		body: string(body),
	}
}

// fetch serves resp from a fake server and returns the client result together
// with the request the client made.
func fetch(t *testing.T, resp fakeResponse) (fng.Reading, *http.Request, error) {
	t.Helper()
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		for k, v := range resp.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		if resp.status != 0 {
			w.WriteHeader(resp.status)
		}
		_, _ = w.Write([]byte(resp.body))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL + "/", HTTP: srv.Client(), Now: func() time.Time { return testNow }}
	r, err := c.Fetch(context.Background())
	return r, got, err
}

// realResponse loads a response of capi.coinglass.com recorded on 2026-09-24,
// when the page showed 72 (Greed).
func realResponse(t *testing.T) fakeResponse {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/coinglass_history_2026-09-24.json")
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	return fakeResponse{headers: rec.Headers, body: string(rec.Body)}
}

func TestFetchRealResponse(t *testing.T) {
	r, _, err := fetch(t, realResponse(t))
	if err != nil {
		t.Fatal(err)
	}
	want := fng.Reading{Value: 72, Time: time.Date(2026, 9, 24, 0, 20, 1, 0, time.UTC), Price: 84400.7}
	if r != want {
		t.Errorf("reading = %+v, want %+v (the value the page showed)", r, want)
	}
}

// The whole real payload backs the way it is read: the first chart is the
// index, the other charts cannot be mistaken for it.
func TestRealResponseStructure(t *testing.T) {
	resp := realResponse(t)
	var env envelope
	if err := json.Unmarshal([]byte(resp.body), &env); err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	for k, v := range resp.headers {
		h.Set(k, v)
	}
	payload, err := decodePayload(h, env.Data, "")
	if err != nil {
		t.Fatal(err)
	}
	var charts []series
	if err := json.Unmarshal(payload, &charts); err != nil {
		t.Fatal(err)
	}
	if len(charts) < 2 {
		t.Fatalf("got %d charts, want the index and the others", len(charts))
	}

	index := charts[0]
	if len(index.Values) == 0 || len(index.Dates) != len(index.Values) || len(index.Prices) != len(index.Values) {
		t.Fatalf("index chart: %d dates, %d prices, %d values", len(index.Dates), len(index.Prices), len(index.Values))
	}
	for i, v := range index.Values {
		if v == nil || *v < 0 || *v > 100 {
			t.Fatalf("index value #%d = %v, want 0..100", i, v)
		}
		if i > 0 && index.Dates[i] <= index.Dates[i-1] {
			t.Fatalf("index dates must increase, #%d: %v after %v", i, index.Dates[i], index.Dates[i-1])
		}
	}
	first, last := time.UnixMilli(int64(index.Dates[0])).UTC(), time.UnixMilli(int64(index.Dates[len(index.Dates)-1])).UTC()
	t.Logf("index chart: %d values from %s to %s", len(index.Values), first.Format(time.DateOnly), last.Format(time.DateOnly))

	for i, c := range charts[1:] {
		outside := false
		for _, v := range c.Values {
			if v != nil && (*v < 0 || *v > 100) {
				outside = true
				break
			}
		}
		if !outside {
			t.Errorf("chart #%d also fits 0..100: the index could be mistaken for it", i+1)
		}
	}
}

func TestFetchDecryptsEveryKnownVersion(t *testing.T) {
	for _, version := range []string{"0", "1", "2", "55", "66", "77"} {
		t.Run("v="+version, func(t *testing.T) {
			r, _, err := fetch(t, encryptedResponse(t, version, historyPayload, true, false))
			if err != nil {
				t.Fatal(err)
			}
			if r != wantReading {
				t.Errorf("reading = %+v, want %+v", r, wantReading)
			}
		})
	}
}

func TestFetchEncodingVariants(t *testing.T) {
	for name, resp := range map[string]fakeResponse{
		"unquoted data key": encryptedResponse(t, "66", historyPayload, false, false),
		"gzip":              encryptedResponse(t, "66", historyPayload, true, true),
	} {
		t.Run(name, func(t *testing.T) {
			r, _, err := fetch(t, resp)
			if err != nil {
				t.Fatal(err)
			}
			if r != wantReading {
				t.Errorf("reading = %+v, want %+v", r, wantReading)
			}
		})
	}
}

func TestFetchSendsPageHeaders(t *testing.T) {
	_, req, err := fetch(t, encryptedResponse(t, "0", historyPayload, true, false))
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodGet || req.URL.Path != historyPath || req.URL.RawQuery != "size=" {
		t.Errorf("request = %s %s, want GET %s?size=", req.Method, req.URL, historyPath)
	}
	for header, want := range map[string]string{
		"encryption":  "true",
		"cache-ts-v2": testTS,
		"language":    "en",
		"Accept":      "application/json",
	} {
		if got := req.Header.Get(header); got != want {
			t.Errorf("header %s = %q, want %q", header, got, want)
		}
	}
}

func TestFetchUnencryptedData(t *testing.T) {
	r, _, err := fetch(t, fakeResponse{body: `{"code":0,"msg":"success","data":` + historyPayload + `}`})
	if err != nil {
		t.Fatal(err)
	}
	if r != wantReading {
		t.Errorf("reading = %+v, want %+v", r, wantReading)
	}
}

func TestFetchSkipsTrailingNulls(t *testing.T) {
	payload := `[{"dates":[1790036401000,1790122801000,1790209201000],"prices":[86429.5,null,null],"values":[40,55,null]}]`
	r, _, err := fetch(t, encryptedResponse(t, "66", payload, true, false))
	if err != nil {
		t.Fatal(err)
	}
	want := fng.Reading{Value: 55, Time: time.UnixMilli(1790122801000).UTC()}
	if r != want {
		t.Errorf("reading = %+v, want %+v", r, want)
	}
}

func TestFetchErrors(t *testing.T) {
	wrongKey := encryptedResponse(t, "66", historyPayload, true, false)
	wrongKey.headers["user"] = encrypt(t, []byte(`"fedcba9876543210"`), []byte("ZDY1MzdkODQ1YTk2"), false)
	unknownVersion := encryptedResponse(t, "66", historyPayload, true, false)
	unknownVersion.headers["v"] = "99"
	brokenUser := encryptedResponse(t, "66", historyPayload, true, false)
	brokenUser.headers["user"] = "garbage"
	notString := fakeResponse{
		headers: map[string]string{"encryption": "true", "v": "66", "user": "x"},
		body:    `{"code":"0","data":[1,2]}`,
	}

	tests := []struct {
		name    string
		resp    fakeResponse
		wantErr string
	}{
		{"http error", fakeResponse{status: http.StatusServiceUnavailable, body: "down"}, "503"},
		{"api error", fakeResponse{body: `{"code":"50001","msg":"too many requests","success":false}`}, "too many requests"},
		{"no data", fakeResponse{body: `{"code":"0","msg":"success","success":true}`}, "no data"},
		{"not json", fakeResponse{body: "<html>"}, "decode response"},
		{"unsupported version", unknownVersion, "unsupported encryption version"},
		{"broken user header", brokenUser, "decrypt data key"},
		{"wrong data key", wrongKey, "decrypt data (v=66)"},
		{"encrypted data is not a string", notString, "not a string"},
		{"empty history", encryptedResponse(t, "66", `[]`, true, false), "empty"},
		{"no values", encryptedResponse(t, "66", `[{"dates":[1],"values":[null]}]`, true, false), "no values"},
		{"value out of range", encryptedResponse(t, "66", `[{"dates":[1],"values":[4360.85]}]`, true, false), "outside of 0..100"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _, err := fetch(t, tt.resp)
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
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Now: time.Now}
	if _, err := c.Fetch(ctx); err == nil {
		t.Fatal("expected an error")
	}
}

// TestLiveFetch queries the real site. It is skipped by default:
// FNG_LIVE_TEST=1 go test ./internal/coinglass -run Live -v
func TestLiveFetch(t *testing.T) {
	if os.Getenv("FNG_LIVE_TEST") == "" {
		t.Skip("set FNG_LIVE_TEST=1 to query coinglass.com")
	}
	r, err := NewClient(&http.Client{Timeout: 30 * time.Second}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Fear & Greed Index: %v (%s) as of %s, BTC %v", r.Value, fng.Classify(r.Value), r.Time, r.Price)
	if age := time.Since(r.Time); age > 7*24*time.Hour {
		t.Errorf("the latest value is %s old", age)
	}
}
