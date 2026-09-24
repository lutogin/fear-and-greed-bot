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
)

// fakeServices stands in for capi.coinglass.com (serving an unencrypted
// response, which the client also accepts) and the Telegram Bot API.
type fakeServices struct {
	coinglass *httptest.Server
	telegram  *httptest.Server

	mu       sync.Mutex
	index    string
	messages []string
}

func newFakeServices(t *testing.T, index string) *fakeServices {
	f := &fakeServices{index: index}
	f.coinglass = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_, _ = io.WriteString(w, `{"code":"0","msg":"success","data":[{"dates":[1790209201000],"prices":[84400.7],"values":[`+f.index+`]}]}`)
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
		f.messages = append(f.messages, req.Text)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(f.coinglass.Close)
	t.Cleanup(f.telegram.Close)
	return f
}

func (f *fakeServices) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
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

// Runs the bot like a cron job would: every run is a new process, so the
// cooldown must come from the state file.
func TestOnceRunsShareCooldownThroughStateFile(t *testing.T) {
	services := newFakeServices(t, "15")
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
	services := newFakeServices(t, "50")
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
	services := newFakeServices(t, "50")
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
	services := newFakeServices(t, "50")
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
