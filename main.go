// Command fngbot watches the Crypto Fear & Greed Index (alternative.me or
// CoinGlass) and the CNN Fear & Greed Index of the US stock market, and
// notifies a Telegram chat when an index reaches its configured key levels.
package main

import (
	"context"
	"errors"
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
	"fear-and-greed-bot/internal/cnn"
	"fear-and-greed-bot/internal/coinglass"
	"fear-and-greed-bot/internal/config"
	"fear-and-greed-bot/internal/dedup"
	"fear-and-greed-bot/internal/fng"
	"fear-and-greed-bot/internal/telegram"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config")
	once := flag.Bool("once", false, "check the index once and exit")
	testMessage := flag.Bool("test-message", false, "send the current index values to Telegram to check the setup, then exit")
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
	cryptoURL   string // the API host of the crypto index source
	stocksURL   string // the API host of the stock index source
	telegramURL string
}

func run(ctx context.Context, log *slog.Logger, opts options) error {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	indexes := newIndexes(cfg, httpClient, opts)
	chatID, topicID := cfg.Telegram.Destination()
	notifier := telegram.New(cfg.Telegram.BotToken, chatID, topicID, httpClient)
	if opts.telegramURL != "" {
		notifier.BaseURL = opts.telegramURL
	}

	if opts.testMessage {
		return sendTestMessage(ctx, log, indexes, notifier)
	}

	store, err := openStore(cfg.Dedup)
	if err != nil {
		return err
	}
	b := bot.New(indexes, notifier, dedup.New(store, cfg.Dedup.Cooldown.Std()), bot.Options{
		Interval: cfg.Interval.Std(),
		Logger:   log,
	})
	if opts.once { // no startup message: under cron it would come with every run
		return b.Check(ctx)
	}

	attrs := []any{"interval", cfg.Interval, "cooldown", cfg.Dedup.Cooldown, "storage", cfg.Dedup.Storage, "chat_id", chatID}
	if topicID != 0 {
		attrs = append(attrs, "topic_id", topicID)
	}
	for _, idx := range indexes {
		attrs = append(attrs, slog.Group(string(idx.Market),
			"source", idx.Source.Provider().Name, "fear_levels", idx.Rules.Fear, "greed_levels", idx.Rules.Greed))
	}
	log.Info("bot started", attrs...)
	b.Run(ctx) // announces the start in Telegram with the current index values
	log.Info("bot stopped")
	return nil
}

// sendTestMessage sends the current value of every index. The message also
// goes out when some index could not be read, to show which one fails.
func sendTestMessage(ctx context.Context, log *slog.Logger, indexes []bot.Index, notifier *telegram.Client) error {
	statuses := bot.Fetch(ctx, indexes)
	if err := notifier.Send(ctx, bot.TestMessage(statuses)); err != nil {
		return err
	}
	var errs []error
	for _, s := range statuses {
		if s.FetchErr != nil {
			errs = append(errs, fmt.Errorf("fetch %s index: %w", s.Market, s.FetchErr))
			continue
		}
		log.Info("test message sent", "index", s.Market, "value", s.Reading.Value)
	}
	return errors.Join(errs...)
}

// newIndexes returns the configured indexes: crypto first, then stocks.
func newIndexes(cfg config.Config, httpClient *http.Client, opts options) []bot.Index {
	var indexes []bot.Index
	if c := cfg.Crypto; c != nil {
		indexes = append(indexes, bot.Index{Market: fng.Crypto, Source: newCryptoSource(c.Source, httpClient, opts.cryptoURL), Rules: c.Rules()})
	}
	if c := cfg.Stocks; c != nil {
		src := cnn.NewClient(httpClient) // the only stock index source
		if opts.stocksURL != "" {
			src.BaseURL = opts.stocksURL
		}
		indexes = append(indexes, bot.Index{Market: fng.Stocks, Source: src, Rules: c.Rules()})
	}
	return indexes
}

// newCryptoSource returns the configured crypto index source; baseURL, if set,
// replaces its API host.
func newCryptoSource(name string, httpClient *http.Client, baseURL string) bot.Source {
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
