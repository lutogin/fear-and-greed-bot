// Package config loads the bot configuration from a YAML file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"fear-and-greed-bot/internal/alert"
)

const (
	// Sources of the crypto index.
	SourceAlternative = "alternative"
	SourceCoinGlass   = "coinglass"
	// Source of the stock market index.
	SourceCNN = "cnn"

	StorageFile   = "file"
	StorageMemory = "memory"

	// MinInterval keeps the bot from hammering the index provider.
	MinInterval = time.Minute

	EnvBotToken    = "TELEGRAM_BOT_TOKEN"
	EnvChatID      = "TELEGRAM_CHAT_ID"
	EnvGroupChatID = "TELEGRAM_GROUP_CHAT_ID"
	EnvTopicID     = "TELEGRAM_TOPIC_ID"
)

type Config struct {
	// Interval between index checks.
	Interval Duration `yaml:"interval"`
	// The watched indexes; a missing section turns its index off.
	Crypto   *IndexConfig   `yaml:"crypto"`
	Stocks   *IndexConfig   `yaml:"stocks"`
	Dedup    DedupConfig    `yaml:"dedup"`
	Telegram TelegramConfig `yaml:"telegram"`

	// The top-level keys of the single-index config, which moved to the crypto
	// section. They are read only to explain the move.
	LegacySource string        `yaml:"source"`
	LegacyAlerts *AlertsConfig `yaml:"alerts"`
}

// IndexConfig describes one watched index.
type IndexConfig struct {
	// Source of the index. Crypto: "alternative" (the alternative.me API) or
	// "coinglass" (the data of the coinglass.com page). Stocks: "cnn".
	Source string       `yaml:"source"`
	Alerts AlertsConfig `yaml:"alerts"`
}

// Rules returns the key levels of the index.
func (c IndexConfig) Rules() alert.Rules {
	return alert.Rules{Fear: c.Alerts.Fear, Greed: c.Alerts.Greed}
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

// TelegramConfig sets where messages go: either ChatID or GroupChatID, the
// latter optionally with the forum topic TopicID.
type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	// ChatID is a user, a group or a channel (@name).
	ChatID string `yaml:"chat_id"`
	// GroupChatID is a group whose topic TopicID receives the messages.
	GroupChatID string `yaml:"group_chat_id"`
	// TopicID is the message_thread_id of the topic; 0 posts to the group itself.
	TopicID TopicID `yaml:"topic_id"`
}

// Destination returns the chat to send messages to and its forum topic, 0 for none.
func (c TelegramConfig) Destination() (chatID string, topicID int64) {
	if c.GroupChatID != "" {
		return c.GroupChatID, int64(c.TopicID)
	}
	return c.ChatID, 0
}

// Default returns the configuration used for options missing from the file.
// The index sources get their defaults in Load, once it is known which
// sections the file has.
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
	if cfg.Crypto != nil && cfg.Crypto.Source == "" {
		cfg.Crypto.Source = SourceAlternative
	}
	if cfg.Stocks != nil && cfg.Stocks.Source == "" {
		cfg.Stocks.Source = SourceCNN
	}
	if v := os.Getenv(EnvBotToken); v != "" {
		cfg.Telegram.BotToken = v
	}
	if v := os.Getenv(EnvChatID); v != "" {
		cfg.Telegram.ChatID = v
	}
	if v := os.Getenv(EnvGroupChatID); v != "" {
		cfg.Telegram.GroupChatID = v
	}
	if v := os.Getenv(EnvTopicID); v != "" {
		id, err := ParseTopicID(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", EnvTopicID, err)
		}
		cfg.Telegram.TopicID = id
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s:\n%w", path, err)
	}
	return cfg, nil
}

// Validate reports all problems of the configuration at once.
func (c Config) Validate() error {
	var errs []error
	if c.LegacySource != "" || c.LegacyAlerts != nil {
		errs = append(errs, errors.New("source and alerts moved to the crypto section: crypto.source, crypto.alerts (see config.example.yaml)"))
	}
	if c.Crypto == nil && c.Stocks == nil {
		errs = append(errs, errors.New("at least one index is required: the crypto or the stocks section"))
	}
	if c.Crypto != nil {
		errs = append(errs, validateIndex("crypto", *c.Crypto, SourceAlternative, SourceCoinGlass))
	}
	if c.Stocks != nil {
		errs = append(errs, validateIndex("stocks", *c.Stocks, SourceCNN))
	}
	if c.Interval.Std() < MinInterval {
		errs = append(errs, fmt.Errorf("interval must be at least %s", MinInterval))
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
	errs = append(errs, c.Telegram.validateDestination())
	return errors.Join(errs...)
}

func (c TelegramConfig) validateDestination() error {
	var errs []error
	switch {
	case c.ChatID == "" && c.GroupChatID == "":
		errs = append(errs, fmt.Errorf("telegram.chat_id or telegram.group_chat_id is required (or set %s or %s)", EnvChatID, EnvGroupChatID))
	case c.ChatID != "" && c.GroupChatID != "":
		errs = append(errs, fmt.Errorf("telegram.chat_id (%s) and telegram.group_chat_id (%s) are mutually exclusive: "+
			"keep chat_id to send to a chat, or group_chat_id with topic_id to send to a group topic", EnvChatID, EnvGroupChatID))
	}
	if c.TopicID < 0 {
		errs = append(errs, fmt.Errorf("telegram.topic_id must be positive, got %d", c.TopicID))
	}
	if c.TopicID != 0 && c.GroupChatID == "" {
		errs = append(errs, errors.New("telegram.topic_id requires telegram.group_chat_id"))
	}
	return errors.Join(errs...)
}

// validateIndex checks the section name of an index, which may use one of sources.
func validateIndex(name string, c IndexConfig, sources ...string) error {
	var errs []error
	if !slices.Contains(sources, c.Source) {
		quoted := make([]string, len(sources))
		for i, s := range sources {
			quoted[i] = strconv.Quote(s)
		}
		errs = append(errs, fmt.Errorf("%s.source must be %s, got %q", name, strings.Join(quoted, " or "), c.Source))
	}
	if err := c.Rules().Validate(); err != nil {
		errs = append(errs, fmt.Errorf("%s.alerts: %w", name, err))
	}
	return errors.Join(errs...)
}

// TopicID is the id of a forum topic. YAML accepts it with or without quotes,
// like chat_id.
type TopicID int64

// ParseTopicID parses the decimal id of a forum topic.
func ParseTopicID(s string) (TopicID, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("topic_id must be an integer, got %q", s)
	}
	return TopicID(id), nil
}

func (t *TopicID) UnmarshalYAML(n *yaml.Node) error {
	id, err := ParseTopicID(n.Value)
	if n.Kind != yaml.ScalarNode {
		err = errors.New("topic_id must be an integer")
	}
	if err != nil {
		// A TypeError lets the decoder report it together with the other problems.
		return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: %v", n.Line, err)}}
	}
	*t = id
	return nil
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
