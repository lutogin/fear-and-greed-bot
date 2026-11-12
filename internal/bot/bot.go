// Package bot periodically checks the indexes and sends notifications.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"fear-and-greed-bot/internal/alert"
	"fear-and-greed-bot/internal/dedup"
	"fear-and-greed-bot/internal/fng"
)

const (
	DefaultInterval = 4 * time.Hour
	// DefaultRetryDelay is how soon a failed check is retried.
	DefaultRetryDelay = 5 * time.Minute
)

// Source provides the current index value.
type Source interface {
	Fetch(ctx context.Context) (fng.Reading, error)
	// Provider is credited next to the data in every message.
	Provider() fng.Provider
}

// Notifier delivers a message to the user.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

// Index is a Fear & Greed index the bot watches.
type Index struct {
	Market fng.Market
	Source Source
	Rules  alert.Rules
}

// Options tune the bot; zero values are replaced by defaults.
type Options struct {
	Interval   time.Duration // between successful checks of an index
	RetryDelay time.Duration // after a failed check of an index, capped by Interval
	Logger     *slog.Logger
	Now        func() time.Time
}

type Bot struct {
	indexes    []Index
	notifier   Notifier
	dedup      *dedup.Deduplicator
	interval   time.Duration
	retryDelay time.Duration
	log        *slog.Logger
	now        func() time.Time
}

func New(indexes []Index, notifier Notifier, dd *dedup.Deduplicator, opts Options) *Bot {
	b := &Bot{
		indexes:    indexes,
		notifier:   notifier,
		dedup:      dd,
		interval:   opts.Interval,
		retryDelay: opts.RetryDelay,
		log:        opts.Logger,
		now:        opts.Now,
	}
	if b.interval <= 0 {
		b.interval = DefaultInterval
	}
	if b.retryDelay <= 0 {
		b.retryDelay = DefaultRetryDelay
	}
	b.retryDelay = min(b.retryDelay, b.interval)
	if b.log == nil {
		b.log = slog.Default()
	}
	if b.now == nil {
		b.now = time.Now
	}
	return b
}

// Status is the current state of an index.
type Status struct {
	Market   fng.Market
	Provider fng.Provider
	Rules    alert.Rules
	Reading  fng.Reading // the current index value, valid when FetchErr is nil
	FetchErr error       // why the current value could not be read
}

// Fetch reads the current value of every index. A failed index does not stop
// the others: its error is in its Status.
func Fetch(ctx context.Context, indexes []Index) []Status {
	statuses := make([]Status, len(indexes))
	for i, idx := range indexes {
		r, err := idx.Source.Fetch(ctx)
		statuses[i] = Status{Market: idx.Market, Provider: idx.Source.Provider(), Rules: idx.Rules, Reading: r, FetchErr: err}
	}
	return statuses
}

// Check checks every index once and sends a notification for each one that
// reached a key level, if the cooldown allows it. A failed index does not stop
// the others; the returned error joins the failures.
func (b *Bot) Check(ctx context.Context) error {
	var errs []error
	for _, idx := range b.indexes {
		if err := b.check(ctx, idx); err != nil {
			errs = append(errs, fmt.Errorf("%s index: %w", idx.Market, err))
		}
	}
	return errors.Join(errs...)
}

func (b *Bot) check(ctx context.Context, idx Index) error {
	r, err := idx.Source.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch index: %w", err)
	}
	return b.process(ctx, idx, r)
}

// process sends a notification if r reached a key level of the index and the
// cooldown allows it. A notification that failed to send is not recorded, so
// the next check tries again.
func (b *Bot) process(ctx context.Context, idx Index, r fng.Reading) error {
	log := b.log.With("index", idx.Market, "value", r.Value, "zone", r.Zone, "as_of", r.Time)

	a, ok := idx.Rules.Evaluate(r.Value)
	if !ok {
		log.Info("index checked, no key level reached")
		return nil
	}
	log = log.With("side", a.Side, "level", a.Level)

	now := b.now()
	if allowed, last := b.dedup.Allow(idx.Market, a, now); !allowed {
		log.Info("key level reached, notification suppressed by cooldown",
			"last_sent_at", last.SentAt, "last_level", last.Level)
		return nil
	}
	if err := b.notifier.Send(ctx, Message(idx.Market, idx.Source.Provider(), r, a)); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	if err := b.dedup.Mark(idx.Market, a, now); err != nil {
		return fmt.Errorf("notification sent, but saving the dedup state failed: %w", err)
	}
	log.Info("notification sent")
	return nil
}

// Run checks the indexes immediately, announces the start in Telegram with the
// results, and then checks every index every interval until ctx is cancelled.
// Each index has its own schedule: a failed check is retried after the retry
// delay without querying the other indexes' sources.
func (b *Bot) Run(ctx context.Context) {
	errs := b.start(ctx)
	if ctx.Err() != nil {
		return
	}
	var wg sync.WaitGroup
	for i, idx := range b.indexes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.watch(ctx, idx, errs[i])
		}()
	}
	wg.Wait()
}

// watch checks idx until ctx is cancelled; err is the result of the previous check.
func (b *Bot) watch(ctx context.Context, idx Index, err error) {
	log := b.log.With("index", idx.Market)
	for {
		wait := b.interval
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait = b.retryDelay
			log.Error("check failed", "err", err, "retry_in", wait)
		}
		log.Debug("waiting for the next check", "in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		err = b.check(ctx, idx)
	}
}

// start is the first check of every index. Their values go into the startup
// message, which is sent before possible alerts. The message is informational:
// if Telegram is unavailable right now, the bot still runs and alerts are
// retried on their own. start returns the check result of each index.
func (b *Bot) start(ctx context.Context) []error {
	statuses := Fetch(ctx, b.indexes)
	if ctx.Err() != nil {
		return nil
	}
	next := b.interval
	for _, s := range statuses {
		if s.FetchErr != nil {
			next = b.retryDelay
		}
	}
	msg := StartMessage(StartInfo{
		Indexes:   statuses,
		Interval:  b.interval,
		Cooldown:  b.dedup.Cooldown(),
		NextCheck: b.now().Add(next),
	})
	if err := b.notifier.Send(ctx, msg); err != nil {
		b.log.Warn("startup notification not sent", "err", err)
	}
	errs := make([]error, len(statuses))
	for i, s := range statuses {
		if s.FetchErr != nil {
			errs[i] = fmt.Errorf("fetch index: %w", s.FetchErr)
			continue
		}
		errs[i] = b.process(ctx, b.indexes[i], s.Reading)
	}
	return errs
}
