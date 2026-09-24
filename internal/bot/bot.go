// Package bot periodically checks the index and sends notifications.
package bot

import (
	"context"
	"fmt"
	"log/slog"
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
}

// Notifier delivers a message to the user.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

// Options tune the bot; zero values are replaced by defaults.
type Options struct {
	Interval   time.Duration // between successful checks
	RetryDelay time.Duration // after a failed check, capped by Interval
	Logger     *slog.Logger
	Now        func() time.Time
}

type Bot struct {
	source     Source
	notifier   Notifier
	rules      alert.Rules
	dedup      *dedup.Deduplicator
	interval   time.Duration
	retryDelay time.Duration
	log        *slog.Logger
	now        func() time.Time
}

func New(source Source, notifier Notifier, rules alert.Rules, dd *dedup.Deduplicator, opts Options) *Bot {
	b := &Bot{
		source:     source,
		notifier:   notifier,
		rules:      rules,
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

// Check fetches the index once and sends a notification if a key level is
// reached and the cooldown allows it.
func (b *Bot) Check(ctx context.Context) error {
	r, err := b.source.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch index: %w", err)
	}
	return b.process(ctx, r)
}

// process sends a notification if r reached a key level and the cooldown
// allows it. A notification that failed to send is not recorded, so the next
// check tries again.
func (b *Bot) process(ctx context.Context, r fng.Reading) error {
	log := b.log.With("value", r.Value, "zone", fng.Classify(r.Value), "as_of", r.Time)

	a, ok := b.rules.Evaluate(r.Value)
	if !ok {
		log.Info("index checked, no key level reached")
		return nil
	}
	log = log.With("side", a.Side, "level", a.Level)

	now := b.now()
	if allowed, last := b.dedup.Allow(a, now); !allowed {
		log.Info("key level reached, notification suppressed by cooldown",
			"last_sent_at", last.SentAt, "last_level", last.Level)
		return nil
	}
	if err := b.notifier.Send(ctx, Message(r, a)); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	if err := b.dedup.Mark(a, now); err != nil {
		return fmt.Errorf("notification sent, but saving the dedup state failed: %w", err)
	}
	log.Info("notification sent")
	return nil
}

// Run checks the index immediately, announces the start in Telegram with the
// result, and then checks every interval until ctx is cancelled. A failed
// check is retried after the retry delay.
func (b *Bot) Run(ctx context.Context) {
	err := b.start(ctx)
	for {
		wait := b.interval
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait = b.retryDelay
			b.log.Error("check failed", "err", err, "retry_in", wait)
		}
		b.log.Debug("waiting for the next check", "in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		err = b.Check(ctx)
	}
}

// start is the first check. Its value goes into the startup message, which is
// sent before a possible alert. The message is informational: if Telegram is
// unavailable right now, the bot still runs and alerts are retried on their own.
func (b *Bot) start(ctx context.Context) error {
	r, fetchErr := b.source.Fetch(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	next := b.interval
	if fetchErr != nil {
		next = b.retryDelay
	}
	msg := StartMessage(StartInfo{
		Reading:   r,
		FetchErr:  fetchErr,
		Rules:     b.rules,
		Interval:  b.interval,
		Cooldown:  b.dedup.Cooldown(),
		NextCheck: b.now().Add(next),
	})
	if err := b.notifier.Send(ctx, msg); err != nil {
		b.log.Warn("startup notification not sent", "err", err)
	}
	if fetchErr != nil {
		return fmt.Errorf("fetch index: %w", fetchErr)
	}
	return b.process(ctx, r)
}
