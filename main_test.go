package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sourceReply writes a response of the fake index source.
type sourceReply func(w http.ResponseWriter)

// indexReply serves value the way api.alternative.me does.
func indexReply(value, classification string) sourceReply {
	return func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"name":"Fear and Greed Index","data":[{"value":"`+value+
			`","value_classification":"`+classification+`","timestamp":"1790294400","time_until_update":"3600"}],"metadata":{"error":null}}`)
	}
}

// recordedAlternativeReply replays a real response of api.alternative.me recorded on 2026-09-25.
func recordedAlternativeReply(t *testing.T) sourceReply {
	t.Helper()
	body, err := os.ReadFile("testdata/alternative_fng_2026-09-25.json")
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter) { _, _ = w.Write(body) }
}

// recordedCoinGlassReply replays a real response of capi.coinglass.com recorded
// on 2026-09-24, when the page showed 72 (Greed).
func recordedCoinGlassReply(t *testing.T) sourceReply {
	t.Helper()
	raw, err := os.ReadFile("testdata/coinglass_history_2026-09-24.json")
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
	return func(w http.ResponseWriter) {
		for k, v := range rec.Headers {
			w.Header().Set(k, v)
		}
		_, _ = w.Write(rec.Body)
	}
}

// cnnReply serves score the way the CNN graphdata endpoint does.
func cnnReply(score, rating string) sourceReply {
	return func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"fear_and_greed":{"score":`+score+`,"rating":"`+rating+
			`","timestamp":"2026-11-12T21:43:31+00:00","previous_close":36.97},"fear_and_greed_historical":{"data":[]}}`)
	}
}

// recordedCNNReply replays a real response of production.dataviz.cnn.io
// recorded on 2026-11-12, with the score 39.94 (fear).
func recordedCNNReply(t *testing.T) sourceReply {
	t.Helper()
	body, err := os.ReadFile("testdata/cnn_graphdata_2026-11-12.json")
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter) { _, _ = w.Write(body) }
}

// teapotReply is how the CNN endpoint turns away clients it takes for bots.
func teapotReply(w http.ResponseWriter) {
	w.WriteHeader(http.StatusTeapot)
	_, _ = io.WriteString(w, "I'm a teapot")
}

// fakeSource stands in for the API of an index source.
type fakeSource struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string // request URIs
}

func newFakeSource(t *testing.T, reply sourceReply) *fakeSource {
	s := &fakeSource{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls = append(s.calls, r.URL.RequestURI())
		s.mu.Unlock()
		reply(w)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *fakeSource) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// fakeServices stands in for the index sources and the Telegram Bot API.
type fakeServices struct {
	crypto   *fakeSource
	stocks   *fakeSource
	telegram *httptest.Server

	mu           sync.Mutex
	messages     []string
	telegramDown int // the next requests to fail with HTTP 500
}

// newFakeServices starts fake sources answering with the given replies;
// the stocks one answers with a neutral value.
func newFakeServices(t *testing.T, crypto sourceReply) *fakeServices {
	return newFakeMarkets(t, crypto, cnnReply("50", "neutral"))
}

func newFakeMarkets(t *testing.T, crypto, stocks sourceReply) *fakeServices {
	f := &fakeServices{crypto: newFakeSource(t, crypto), stocks: newFakeSource(t, stocks)}
	f.telegram = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		if r.URL.Path != "/bottest:token/sendMessage" || json.NewDecoder(r.Body).Decode(&req) != nil || req.ChatID != "42" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"unexpected request"}`)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.telegramDown > 0 {
			f.telegramDown--
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
			return
		}
		f.messages = append(f.messages, req.Text)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(f.telegram.Close)
	return f
}

func (f *fakeServices) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func (f *fakeServices) failTelegram(requests int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.telegramDown = requests
}

// waitForMessages waits until at least n messages have been delivered.
func (f *fakeServices) waitForMessages(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		sent := f.sent()
		if len(sent) >= n {
			return sent
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivered %d messages, want %d: %q", len(sent), n, sent)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fakeServices) options(configPath string) options {
	return options{configPath: configPath, cryptoURL: f.crypto.URL, stocksURL: f.stocks.URL, telegramURL: f.telegram.URL}
}

// Index sections of the test configs.
const (
	cryptoIndex = "crypto:\n  alerts:\n    fear: [20]\n    greed: [80]\n"
	stocksIndex = "stocks:\n  alerts:\n    fear: [25]\n    greed: [75]\n"
	coinGlass   = "crypto:\n  source: coinglass\n  alerts:\n    fear: [20]\n    greed: [80]\n"
)

// writeConfig writes a config watching the given index sections.
func writeConfig(t *testing.T, statePath string, indexes ...string) string {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	if len(indexes) == 0 {
		indexes = []string{cryptoIndex}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := strings.Join(indexes, "") + `
dedup:
  cooldown: 2d
  file: ` + statePath + `
telegram:
  bot_token: "test:token"
  chat_id: 42
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// startDaemon runs the bot the long-running way (without -once) until the test ends.
func startDaemon(t *testing.T, opts options) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, quietLogger(), opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the bot did not stop")
		}
	})
}

// The whole bot, by default reading alternative.me, on a real response of its API.
func TestDaemonStartupMessageFromRealAlternativeResponse(t *testing.T) {
	services := newFakeServices(t, recordedAlternativeReply(t))
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"))))

	msg := services.waitForMessages(t, 1)[0]
	t.Logf("startup message:\n%s", msg)
	for _, want := range []string{
		"🚀 <b>Fear &amp; Greed бот запущен</b>",
		"🪙 <b>Крипто: 71</b> (Greed)",
		`Данные <a href="https://alternative.me/crypto/fear-and-greed-index/">alternative.me</a> на 25.09.2026 00:00 UTC`,
		"Пороги: страх ≤ 20 · жадность ≥ 80",
		"Проверка индекса: с интервалом 4 часа, следующая ",
		"Пауза между повторными уведомлениями: 2 дня",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup message does not contain %q", want)
		}
	}
	if strings.Contains(msg, "Акции") {
		t.Error("the stocks index is not configured and must not be reported")
	}
	if got := services.crypto.requests(); len(got) != 1 || got[0] != "/fng/?limit=1" {
		t.Errorf("source requests = %q, want one GET /fng/?limit=1", got)
	}
}

// The whole bot with source: coinglass on a real coinglass.com response: the
// startup message must show the value the page showed.
func TestDaemonStartupMessageFromRealCoinGlassResponse(t *testing.T) {
	services := newFakeServices(t, recordedCoinGlassReply(t))
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"), coinGlass)))

	msg := services.waitForMessages(t, 1)[0]
	t.Logf("startup message:\n%s", msg)
	for _, want := range []string{
		"🪙 <b>Крипто: 72</b> (Greed)",
		"BTC: $84,401",
		`Данные <a href="https://www.coinglass.com/pro/i/FearGreedIndex">CoinGlass</a> на 24.09.2026 00:20 UTC`,
		"Пороги: страх ≤ 20",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup message does not contain %q", want)
		}
	}
	if got := services.crypto.requests(); len(got) != 1 || !strings.HasPrefix(got[0], "/api/index/history") {
		t.Errorf("source requests = %q, want one to /api/index/history", got)
	}
}

func TestDaemonAnnouncesStartBeforeAlert(t *testing.T) {
	services := newFakeServices(t, indexReply("15", "Extreme Fear"))
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"))))

	sent := services.waitForMessages(t, 2)
	if !strings.Contains(sent[0], "Крипто: 15</b> (Extreme Fear)") {
		t.Errorf("first message must be the startup one with the current value, got %q", sent[0])
	}
	if !strings.Contains(sent[1], "Крипто — Fear &amp; Greed Index: 15") || !strings.Contains(sent[1], "≤ 20") ||
		!strings.Contains(sent[1], ">alternative.me</a>") {
		t.Errorf("the alert must follow the startup message, got %q", sent[1])
	}
	if got := services.crypto.requests(); len(got) != 1 {
		t.Errorf("source requested %d times, want 1 for both messages", len(got))
	}
}

// changedEncryptionReply is what the bot sees after coinglass.com changes its encryption.
func changedEncryptionReply(w http.ResponseWriter) {
	w.Header().Set("encryption", "true")
	w.Header().Set("user", "bmV3IGtleQ==")
	w.Header().Set("v", "88")
	_, _ = io.WriteString(w, `{"code":"0","msg":"success","data":"AAAA"}`)
}

func TestDaemonReportsUnreadableIndexAtStartup(t *testing.T) {
	services := newFakeServices(t, changedEncryptionReply)
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"), coinGlass)))

	msg := services.waitForMessages(t, 1)[0]
	for _, want := range []string{"⚠️ Не удалось получить индекс с ", ">CoinGlass</a>: unsupported encryption version", "страх ≤ 20"} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup message %q does not contain %q", msg, want)
		}
	}
}

func TestDaemonRunsWhenStartupMessageFails(t *testing.T) {
	services := newFakeServices(t, indexReply("15", "Extreme Fear"))
	services.failTelegram(1) // only the startup message is lost
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"))))

	sent := services.waitForMessages(t, 1)
	if !strings.Contains(sent[0], "Крипто — Fear &amp; Greed Index: 15") {
		t.Errorf("the first check must still send the alert, got %q", sent[0])
	}
}

// Runs the bot like a cron job would: every run is a new process, so the
// cooldown must come from the state file. -once sends no startup message.
func TestOnceRunsShareCooldownThroughStateFile(t *testing.T) {
	services := newFakeServices(t, indexReply("15", "Extreme Fear"))
	statePath := filepath.Join(t.TempDir(), "state", "state.json")
	opts := services.options(writeConfig(t, statePath))
	opts.once = true

	for range 3 {
		if err := run(context.Background(), quietLogger(), opts); err != nil {
			t.Fatal(err)
		}
	}
	sent := services.sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d notifications, want 1: %q", len(sent), sent)
	}
	if !strings.Contains(sent[0], "Крипто — Fear &amp; Greed Index: 15") || !strings.Contains(sent[0], "≤ 20") {
		t.Errorf("unexpected notification %q", sent[0])
	}
	state, err := os.ReadFile(statePath)
	if err != nil || !strings.Contains(string(state), `"crypto.fear"`) {
		t.Errorf("state file = %s, %v; want a fear record", state, err)
	}
}

func TestOnceWithoutKeyLevelSendsNothing(t *testing.T) {
	services := newFakeServices(t, indexReply("50", "Neutral"))
	statePath := filepath.Join(t.TempDir(), "state.json")
	opts := services.options(writeConfig(t, statePath))
	opts.once = true
	if err := run(context.Background(), quietLogger(), opts); err != nil {
		t.Fatal(err)
	}
	if sent := services.sent(); len(sent) != 0 {
		t.Errorf("sent %q, want nothing", sent)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state file must not be created without notifications, stat err = %v", err)
	}
}

func TestTestMessageFlag(t *testing.T) {
	services := newFakeServices(t, indexReply("50", "Neutral"))
	opts := services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json")))
	opts.testMessage = true
	if err := run(context.Background(), quietLogger(), opts); err != nil {
		t.Fatal(err)
	}
	sent := services.sent()
	if len(sent) != 1 || !strings.Contains(sent[0], "✅") || !strings.Contains(sent[0], "Крипто: 50</b> (Neutral)") {
		t.Fatalf("sent %q, want one test message", sent)
	}
}

// The whole bot watching both markets, on real responses of alternative.me and CNN.
func TestDaemonStartupMessageWithBothIndexes(t *testing.T) {
	services := newFakeMarkets(t, recordedAlternativeReply(t), recordedCNNReply(t))
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"), cryptoIndex, stocksIndex)))

	msg := services.waitForMessages(t, 1)[0]
	t.Logf("startup message:\n%s", msg)
	for _, want := range []string{
		"🪙 <b>Крипто: 71</b> (Greed)\n" +
			`Данные <a href="https://alternative.me/crypto/fear-and-greed-index/">alternative.me</a> на 25.09.2026 00:00 UTC` + "\n" +
			"Пороги: страх ≤ 20 · жадность ≥ 80",
		"📈 <b>Акции США: 40</b> (Fear)\n" +
			`Данные <a href="https://edition.cnn.com/markets/fear-and-greed">CNN</a> на 12.11.2026 21:43 UTC` + "\n" +
			"Пороги: страх ≤ 25 · жадность ≥ 75",
		"Проверка индексов: с интервалом 4 часа",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup message does not contain %q", want)
		}
	}
	if got := services.stocks.requests(); len(got) != 1 || got[0] != "/index/fearandgreed/graphdata" {
		t.Errorf("stocks source requests = %q, want one GET /index/fearandgreed/graphdata", got)
	}
}

func TestDaemonAlertsAboutStocks(t *testing.T) {
	services := newFakeMarkets(t, indexReply("50", "Neutral"), cnnReply("17.6", "extreme fear"))
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"), cryptoIndex, stocksIndex)))

	sent := services.waitForMessages(t, 2)
	want := "😱 <b>Акции США — Fear &amp; Greed Index: 18</b> (Extreme Fear)\n" +
		"Достигнут ключевой уровень страха: ≤ 25\n" +
		`Данные <a href="https://edition.cnn.com/markets/fear-and-greed">CNN</a> на 12.11.2026 21:43 UTC`
	if sent[1] != want {
		t.Errorf("alert:\n%s\nwant:\n%s", sent[1], want)
	}
}

func TestOnceAlertsAboutBothIndexes(t *testing.T) {
	services := newFakeMarkets(t, indexReply("15", "Extreme Fear"), cnnReply("80", "extreme greed"))
	statePath := filepath.Join(t.TempDir(), "state.json")
	opts := services.options(writeConfig(t, statePath, cryptoIndex, stocksIndex))
	opts.once = true

	for range 2 {
		if err := run(context.Background(), quietLogger(), opts); err != nil {
			t.Fatal(err)
		}
	}
	sent := services.sent()
	if len(sent) != 2 || !strings.Contains(sent[0], "Крипто — Fear &amp; Greed Index: 15") ||
		!strings.Contains(sent[1], "Акции США — Fear &amp; Greed Index: 80") || !strings.Contains(sent[1], "≥ 75") {
		t.Fatalf("sent %q, want one crypto and one stocks alert", sent)
	}
	state, err := os.ReadFile(statePath)
	if err != nil || !strings.Contains(string(state), `"crypto.fear"`) || !strings.Contains(string(state), `"stocks.greed"`) {
		t.Errorf("state file = %s, %v; want records of both indexes", state, err)
	}
}

func TestOnceReportsFailedIndexAndAlertsAboutTheOther(t *testing.T) {
	services := newFakeMarkets(t, indexReply("15", "Extreme Fear"), teapotReply)
	opts := services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"), cryptoIndex, stocksIndex))
	opts.once = true
	err := run(context.Background(), quietLogger(), opts)
	if err == nil || !strings.Contains(err.Error(), "stocks index: fetch index: unexpected HTTP status 418") {
		t.Fatalf("err = %v, want the stocks fetch error", err)
	}
	if sent := services.sent(); len(sent) != 1 || !strings.Contains(sent[0], "Крипто — Fear &amp; Greed Index: 15") {
		t.Errorf("sent %q, want the crypto alert", sent)
	}
}

func TestTestMessageFlagWithBothIndexes(t *testing.T) {
	services := newFakeMarkets(t, indexReply("50", "Neutral"), teapotReply)
	opts := services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"), cryptoIndex, stocksIndex))
	opts.testMessage = true
	err := run(context.Background(), quietLogger(), opts)
	if err == nil || !strings.Contains(err.Error(), "fetch stocks index: unexpected HTTP status 418") {
		t.Errorf("err = %v, want the stocks fetch error", err)
	}
	sent := services.sent()
	if len(sent) != 1 || !strings.Contains(sent[0], "Крипто: 50</b> (Neutral)") ||
		!strings.Contains(sent[0], "📈 <b>Акции США</b>\n⚠️ Не удалось получить индекс с ") {
		t.Fatalf("sent %q, want one test message showing both indexes", sent)
	}
}

func TestRunStopsOnCancelledContext(t *testing.T) {
	services := newFakeServices(t, indexReply("50", "Neutral"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, quietLogger(), services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json")))); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("alerts:\n  fear: [20]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), quietLogger(), options{configPath: path, once: true})
	if err == nil || !strings.Contains(err.Error(), "telegram.bot_token") {
		t.Fatalf("err = %v, want a config error", err)
	}
}
