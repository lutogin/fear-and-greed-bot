// Package config loads the bot configuration from a YAML file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"fear-and-greed-bot/internal/alert"
)

const (
	StorageFile   = "file"
	StorageMemory = "memory"

	// MinInterval keeps the bot from hammering coinglass.com.
	MinInterval = time.Minute

	EnvBotToken = "TELEGRAM_BOT_TOKEN"
	EnvChatID   = "TELEGRAM_CHAT_ID"
)

type Config struct {
	// Interval between index checks.
	Interval Duration       `yaml:"interval"`
	Alerts   AlertsConfig   `yaml:"alerts"`
	Dedup    DedupConfig    `yaml:"dedup"`
	Telegram TelegramConfig `yaml:"telegram"`
}

// AlertsConfig holds the key levels: a fear level fires when the index is at or
// below it, a greed level when the index is at or above it.
type AlertsConfig struct {
	Fear  []float64 `yaml:"fear"`
	Greed []float64 `yaml:"greed"`
}

type DedupConfig struct {
	// Cooldown is the minimum time between notifications about the same side.
	Cooldown Duration `yaml:"cooldown"`
	// Storage is "file" (survives restarts) or "memory".
	Storage string `yaml:"storage"`
	// File is the JSON state file used by the "file" storage.
	File string `yaml:"file"`
}

type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

// Default returns the configuration used for options missing from the file.
func Default() Config {
	return Config{
		Interval: Duration(4 * time.Hour),
		Dedup: DedupConfig{
			Cooldown: Duration(48 * time.Hour),
			Storage:  StorageFile,
			File:     "data/state.json",
		},
	}
}

// Load reads the file at path on top of the defaults, applies the environment
// overrides and validates the result.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // report typos instead of silently ignoring them
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if v := os.Getenv(EnvBotToken); v != "" {
		cfg.Telegram.BotToken = v
	}
	if v := os.Getenv(EnvChatID); v != "" {
		cfg.Telegram.ChatID = v
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s:\n%w", path, err)
	}
	return cfg, nil
}

// Validate reports all problems of the configuration at once.
func (c Config) Validate() error {
	var errs []error
	if c.Interval.Std() < MinInterval {
		errs = append(errs, fmt.Errorf("interval must be at least %s", MinInterval))
	}
	if err := c.Rules().Validate(); err != nil {
		errs = append(errs, fmt.Errorf("alerts: %w", err))
	}
	if c.Dedup.Cooldown < 0 {
		errs = append(errs, errors.New("dedup.cooldown must not be negative"))
	}
	switch c.Dedup.Storage {
	case StorageMemory:
	case StorageFile:
		if c.Dedup.File == "" {
			errs = append(errs, errors.New("dedup.file is required for the file storage"))
		}
	default:
		errs = append(errs, fmt.Errorf("dedup.storage must be %q or %q, got %q", StorageFile, StorageMemory, c.Dedup.Storage))
	}
	if c.Telegram.BotToken == "" {
		errs = append(errs, fmt.Errorf("telegram.bot_token is required (or set %s)", EnvBotToken))
	}
	if c.Telegram.ChatID == "" {
		errs = append(errs, fmt.Errorf("telegram.chat_id is required (or set %s)", EnvChatID))
	}
	return errors.Join(errs...)
}

// Rules returns the configured key levels.
func (c Config) Rules() alert.Rules {
	return alert.Rules{Fear: c.Alerts.Fear, Greed: c.Alerts.Greed}
}

// Duration is a time.Duration that also accepts days in YAML, e.g. "2d" or "1d12h".
type Duration time.Duration

func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseDuration(n.Value)
	if err != nil {
		// A TypeError lets the decoder report it together with the other problems.
		return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: %v", n.Line, err)}}
	}
	*d = Duration(v)
	return nil
}

// maxDays keeps day durations far from the time.Duration overflow (~292 years).
const maxDays = 36500

// ParseDuration parses a Go duration ("4h", "90m") optionally prefixed with a
// number of days ("2d", "1d12h", "0.5d").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	days, rest, found := strings.Cut(s, "d")
	if !found {
		return time.ParseDuration(s)
	}
	n, err := strconv.ParseFloat(days, 64)
	if err != nil || !(n >= 0 && n <= maxDays) { // also rejects NaN
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	d := time.Duration(n * float64(24*time.Hour))
	if rest != "" {
		r, err := time.ParseDuration(rest)
		if err != nil || r < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		d += r
	}
	return d, nil
}
