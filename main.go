// Command fngbot watches the Crypto Fear & Greed Index (alternative.me or
// CoinGlass) and notifies a Telegram chat when the index reaches configured key levels.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fear-and-greed-bot/internal/alternative"
	"fear-and-greed-bot/internal/bot"
	"fear-and-greed-bot/internal/coinglass"
	"fear-and-greed-bot/internal/config"
	"fear-and-greed-bot/internal/dedup"
	"fear-and-greed-bot/internal/telegram"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config")
	once := flag.Bool("once", false, "check the index once and exit")
	testMessage := flag.Bool("test-message", false, "send the current index value to Telegram to check the setup, then exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := options{configPath: *configPath, once: *once, testMessage: *testMessage}
	if err := run(ctx, log, opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type options struct {
	configPath  string
	once        bool
	testMessage bool

	// Service URLs, empty for the real ones; tests point them to fakes.
	sourceURL   string
	telegramURL string
}

func run(ctx context.Context, log *slog.Logger, opts options) error {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	source := newSource(cfg.Source, httpClient, opts.sourceURL)
	notifier := telegram.New(cfg.Telegram.BotToken, cfg.Telegram.ChatID, httpClient)
	if opts.telegramURL != "" {
		notifier.BaseURL = opts.telegramURL
	}

	if opts.testMessage {
		r, err := source.Fetch(ctx)
		if err != nil {
			return fmt.Errorf("fetch index: %w", err)
		}
		if err := notifier.Send(ctx, bot.TestMessage(source.Provider(), r)); err != nil {
			return err
		}
		log.Info("test message sent", "value", r.Value)
		return nil
	}

	store, err := openStore(cfg.Dedup)
	if err != nil {
		return err
	}
	b := bot.New(source, notifier, cfg.Rules(), dedup.New(store, cfg.Dedup.Cooldown.Std()), bot.Options{
		Interval: cfg.Interval.Std(),
		Logger:   log,
	})
	if opts.once { // no startup message: under cron it would come with every run
		return b.Check(ctx)
	}

	log.Info("bot started",
		"interval", cfg.Interval, "fear_levels", cfg.Alerts.Fear, "greed_levels", cfg.Alerts.Greed,
		"cooldown", cfg.Dedup.Cooldown, "storage", cfg.Dedup.Storage)
	b.Run(ctx) // announces the start in Telegram with the current index value
	log.Info("bot stopped")
	return nil
}

// newSource returns the configured index source; baseURL, if set, replaces its API host.
func newSource(name string, httpClient *http.Client, baseURL string) bot.Source {
	if name == config.SourceCoinGlass {
		c := coinglass.NewClient(httpClient)
		if baseURL != "" {
			c.BaseURL = baseURL
		}
		return c
	}
	c := alternative.NewClient(httpClient)
	if baseURL != "" {
		c.BaseURL = baseURL
	}
	return c
}

func openStore(c config.DedupConfig) (dedup.Store, error) {
	if c.Storage == config.StorageMemory {
		return dedup.NewMemoryStore(), nil
	}
	return dedup.OpenFileStore(c.File)
}
