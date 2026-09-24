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

// coinglassReply writes a response of the fake capi.coinglass.com.
type coinglassReply func(w http.ResponseWriter)

// indexReply serves value as an unencrypted response, which the client also accepts.
func indexReply(value string) coinglassReply {
	return func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"code":"0","msg":"success","data":[{"dates":[1790209201000],"prices":[84400.7],"values":[`+value+`]}]}`)
	}
}

// recordedReply replays a real response recorded on 2026-09-24, when the page showed 72 (Greed).
func recordedReply(t *testing.T) coinglassReply {
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

// changedEncryptionReply is what the bot sees after coinglass.com changes its encryption.
func changedEncryptionReply(w http.ResponseWriter) {
	w.Header().Set("encryption", "true")
	w.Header().Set("user", "bmV3IGtleQ==")
	w.Header().Set("v", "88")
	_, _ = io.WriteString(w, `{"code":"0","msg":"success","data":"AAAA"}`)
}

// fakeServices stands in for capi.coinglass.com and the Telegram Bot API.
type fakeServices struct {
	coinglass *httptest.Server
	telegram  *httptest.Server

	mu             sync.Mutex
	coinglassCalls int
	messages       []string
	telegramDown   int // the next requests to fail with HTTP 500
}

func newFakeServices(t *testing.T, reply coinglassReply) *fakeServices {
	f := &fakeServices{}
	f.coinglass = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.coinglassCalls++
		f.mu.Unlock()
		reply(w)
	}))
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
	t.Cleanup(f.coinglass.Close)
	t.Cleanup(f.telegram.Close)
	return f
}

func (f *fakeServices) coinglassRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.coinglassCalls
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
	return options{configPath: configPath, coinglassURL: f.coinglass.URL, telegramURL: f.telegram.URL}
}

func writeConfig(t *testing.T, statePath string) string {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := `
alerts:
  fear: [20]
  greed: [80]
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

// The whole bot on a real coinglass.com response: the startup message must
// show the value the page showed.
func TestDaemonStartupMessageFromRealResponse(t *testing.T) {
	services := newFakeServices(t, recordedReply(t))
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"))))

	msg := services.waitForMessages(t, 1)[0]
	t.Logf("startup message:\n%s", msg)
	for _, want := range []string{
		"🚀 <b>Fear &amp; Greed бот запущен</b>",
		"Индекс сейчас: <b>72</b> (Greed)",
		"BTC: $84,401",
		"Данные на 24.09.2026 00:20 UTC",
		"😱 страх: ≤ 20",
		"🤑 жадность: ≥ 80",
		"Проверка индекса: с интервалом 4 часа, следующая ",
		"Пауза между повторными уведомлениями: 2 дня",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup message does not contain %q", want)
		}
	}
}

func TestDaemonAnnouncesStartBeforeAlert(t *testing.T) {
	services := newFakeServices(t, indexReply("15"))
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"))))

	sent := services.waitForMessages(t, 2)
	if !strings.Contains(sent[0], "Индекс сейчас: <b>15</b> (Extreme Fear)") {
		t.Errorf("first message must be the startup one with the current value, got %q", sent[0])
	}
	if !strings.Contains(sent[1], "Fear &amp; Greed Index: 15") || !strings.Contains(sent[1], "≤ 20") {
		t.Errorf("the alert must follow the startup message, got %q", sent[1])
	}
	if got := services.coinglassRequests(); got != 1 {
		t.Errorf("coinglass requested %d times, want 1 for both messages", got)
	}
}

func TestDaemonReportsUnreadableIndexAtStartup(t *testing.T) {
	services := newFakeServices(t, changedEncryptionReply)
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"))))

	msg := services.waitForMessages(t, 1)[0]
	for _, want := range []string{"⚠️ Не удалось получить индекс", "unsupported encryption version", "страх: ≤ 20"} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup message %q does not contain %q", msg, want)
		}
	}
}

func TestDaemonRunsWhenStartupMessageFails(t *testing.T) {
	services := newFakeServices(t, indexReply("15"))
	services.failTelegram(1) // only the startup message is lost
	startDaemon(t, services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json"))))

	sent := services.waitForMessages(t, 1)
	if !strings.Contains(sent[0], "Fear &amp; Greed Index: 15") {
		t.Errorf("the first check must still send the alert, got %q", sent[0])
	}
}

// Runs the bot like a cron job would: every run is a new process, so the
// cooldown must come from the state file. -once sends no startup message.
func TestOnceRunsShareCooldownThroughStateFile(t *testing.T) {
	services := newFakeServices(t, indexReply("15"))
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
	if !strings.Contains(sent[0], "Fear &amp; Greed Index: 15") || !strings.Contains(sent[0], "≤ 20") {
		t.Errorf("unexpected notification %q", sent[0])
	}
	state, err := os.ReadFile(statePath)
	if err != nil || !strings.Contains(string(state), `"fear"`) {
		t.Errorf("state file = %s, %v; want a fear record", state, err)
	}
}

func TestOnceWithoutKeyLevelSendsNothing(t *testing.T) {
	services := newFakeServices(t, indexReply("50"))
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
	services := newFakeServices(t, indexReply("50"))
	opts := services.options(writeConfig(t, filepath.Join(t.TempDir(), "state.json")))
	opts.testMessage = true
	if err := run(context.Background(), quietLogger(), opts); err != nil {
		t.Fatal(err)
	}
	sent := services.sent()
	if len(sent) != 1 || !strings.Contains(sent[0], "✅") || !strings.Contains(sent[0], "(Neutral)") {
		t.Fatalf("sent %q, want one test message", sent)
	}
}

func TestRunStopsOnCancelledContext(t *testing.T) {
	services := newFakeServices(t, indexReply("50"))
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
