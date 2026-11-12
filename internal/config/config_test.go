package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// clearEnv makes the test independent of the developer's environment.
func clearEnv(t *testing.T) {
	t.Setenv(EnvBotToken, "")
	t.Setenv(EnvChatID, "")
}

func TestLoad(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
# every four hours
interval: 4h
crypto:
  source: coinglass
  alerts:
    fear: [25, 10]
    greed: [75]
stocks:
  source: cnn
  alerts:
    fear: [20]
    greed: [80, 90]
dedup:
  cooldown: 2d
  storage: file
  file: /var/lib/fngbot/state.json
telegram:
  bot_token: "123:abc"
  chat_id: -1001234567890   # unquoted numbers work too
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Interval.Std() != 4*time.Hour {
		t.Errorf("interval = %s", cfg.Interval)
	}
	if c := cfg.Crypto; c == nil || c.Source != SourceCoinGlass ||
		!slices.Equal(c.Alerts.Fear, []float64{25, 10}) || !slices.Equal(c.Alerts.Greed, []float64{75}) {
		t.Errorf("crypto = %+v", c)
	}
	if c := cfg.Stocks; c == nil || c.Source != SourceCNN ||
		!slices.Equal(c.Alerts.Fear, []float64{20}) || !slices.Equal(c.Alerts.Greed, []float64{80, 90}) {
		t.Errorf("stocks = %+v", c)
	}
	if cfg.Dedup.Cooldown.Std() != 48*time.Hour || cfg.Dedup.Storage != StorageFile || cfg.Dedup.File != "/var/lib/fngbot/state.json" {
		t.Errorf("dedup = %+v", cfg.Dedup)
	}
	if cfg.Telegram.BotToken != "123:abc" || cfg.Telegram.ChatID != "-1001234567890" {
		t.Errorf("telegram = %+v", cfg.Telegram)
	}
	rules := cfg.Stocks.Rules()
	if !slices.Equal(rules.Fear, cfg.Stocks.Alerts.Fear) || !slices.Equal(rules.Greed, cfg.Stocks.Alerts.Greed) {
		t.Errorf("rules = %+v", rules)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(writeConfig(t, `
crypto:
  alerts:
    fear: [20]
stocks:
  alerts:
    greed: [80]
telegram:
  bot_token: "123:abc"
  chat_id: "42"
`))
	if err != nil {
		t.Fatal(err)
	}
	def := Default()
	if cfg.Crypto.Source != SourceAlternative || cfg.Stocks.Source != SourceCNN || cfg.Interval != def.Interval || cfg.Dedup != def.Dedup {
		t.Errorf("got sources %q and %q, interval %s, dedup %+v; want the defaults alternative, cnn, %s, %+v",
			cfg.Crypto.Source, cfg.Stocks.Source, cfg.Interval, cfg.Dedup, def.Interval, def.Dedup)
	}
	if len(cfg.Crypto.Alerts.Greed) != 0 || len(cfg.Stocks.Alerts.Fear) != 0 {
		t.Errorf("levels must not get defaults, got %+v and %+v", cfg.Crypto.Alerts, cfg.Stocks.Alerts)
	}
}

func TestLoadSingleIndex(t *testing.T) {
	clearEnv(t)
	for _, tt := range []struct{ name, section string }{{"crypto only", "crypto"}, {"stocks only", "stocks"}} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tt.section+":\n  alerts:\n    fear: [20]\ntelegram:\n  bot_token: x\n  chat_id: y\n"))
			if err != nil {
				t.Fatal(err)
			}
			if (cfg.Crypto != nil) != (tt.section == "crypto") || (cfg.Stocks != nil) != (tt.section == "stocks") {
				t.Errorf("crypto = %+v, stocks = %+v; want only %s", cfg.Crypto, cfg.Stocks, tt.section)
			}
		})
	}
}

func TestLoadEnvironmentOverrides(t *testing.T) {
	t.Setenv(EnvBotToken, "env:token")
	t.Setenv(EnvChatID, "777")
	cfg, err := Load(writeConfig(t, `
crypto:
  alerts:
    greed: [80]
telegram:
  bot_token: "file:token"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.BotToken != "env:token" || cfg.Telegram.ChatID != "777" {
		t.Errorf("telegram = %+v, want values from the environment", cfg.Telegram)
	}
}

func TestLoadErrors(t *testing.T) {
	clearEnv(t)
	const valid = `
crypto:
  alerts:
    fear: [25]
    greed: [75]
telegram:
  bot_token: "123:abc"
  chat_id: "42"
`
	tests := []struct {
		name    string
		config  string
		wantErr []string
	}{
		{"typo in a key", valid + "intreval: 4h\n", []string{"intreval"}},
		{"unknown crypto source", "crypto:\n  source: binance\n  alerts:\n    fear: [20]\n", []string{`crypto.source must be "alternative" or "coinglass", got "binance"`}},
		{"crypto source for stocks", "stocks:\n  source: alternative\n  alerts:\n    fear: [20]\n", []string{`stocks.source must be "cnn", got "alternative"`}},
		{"single-index config", "source: coinglass\nalerts:\n  fear: [20]\ntelegram:\n  bot_token: x\n  chat_id: y\n",
			[]string{"moved to the crypto section", "at least one index"}},
		{"bad duration", valid + "interval: soon\n", []string{"invalid duration"}},
		{"all decode errors at once", valid + "intreval: 4h\ndedup:\n  cooldown: 2weeks\n", []string{"intreval", "2weeks"}},
		{"too frequent", valid + "interval: 10s\n", []string{"at least 1m"}},
		{"negative cooldown", valid + "dedup:\n  cooldown: -1h\n", []string{"cooldown"}},
		{"unknown storage", valid + "dedup:\n  storage: redis\n", []string{"dedup.storage"}},
		{"file storage without a file", valid + "dedup:\n  file: \"\"\n", []string{"dedup.file"}},
		{"no telegram", "crypto:\n  alerts:\n    fear: [25]\n", []string{"telegram.bot_token", "telegram.chat_id"}},
		{"no indexes", "telegram:\n  bot_token: x\n  chat_id: y\n", []string{"at least one index"}},
		{"index without alerts", "stocks:\n  source: cnn\ntelegram:\n  bot_token: x\n  chat_id: y\n", []string{"stocks.alerts: at least one"}},
		{"overlapping alerts", "stocks:\n  alerts:\n    fear: [60]\n    greed: [40]\ntelegram:\n  bot_token: x\n  chat_id: y\n", []string{"stocks.alerts: fear levels must be lower than greed"}},
		{"level out of range", "crypto:\n  alerts:\n    greed: [150]\ntelegram:\n  bot_token: x\n  chat_id: y\n", []string{"crypto.alerts: level 150 is outside of 0..100"}},
		{"empty file", "", []string{"telegram.bot_token", "at least one index"}},
		{"not yaml", "crypto: [", []string{"parse config"}},
		{"typo in an index", "crypto:\n  alert:\n    fear: [20]\n", []string{"alert"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.config))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Crypto == nil || cfg.Crypto.Source != SourceAlternative || cfg.Stocks == nil || cfg.Stocks.Source != SourceCNN ||
		cfg.Interval.Std() != 4*time.Hour || cfg.Dedup.Cooldown.Std() != 48*time.Hour {
		t.Errorf("example config: crypto %+v, stocks %+v, interval %s, cooldown %s; want alternative and cnn, 4h and 2d",
			cfg.Crypto, cfg.Stocks, cfg.Interval, cfg.Dedup.Cooldown)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"4h", 4 * time.Hour},
		{"90m", 90 * time.Minute},
		{"2d", 48 * time.Hour},
		{"1d12h", 36 * time.Hour},
		{"0.5d", 12 * time.Hour},
		{" 3d ", 72 * time.Hour},
		{"0", 0},
	}
	for _, tt := range tests {
		got, err := ParseDuration(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParseDuration(%q) = %s, %v; want %s", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "d", "xd", "-1d", "NaNd", "1d-2h", "1dx", "99999999d", "4"} {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %s, want an error", in, got)
		}
	}
}
