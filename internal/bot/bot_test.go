package bot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"fear-and-greed-bot/internal/alert"
	"fear-and-greed-bot/internal/dedup"
	"fear-and-greed-bot/internal/fng"
)

// fakeSource returns the queued values one by one, repeating the last one.
type fakeSource struct {
	mu     sync.Mutex
	values []float64
	errs   []error // returned by the first calls, before the values
	calls  int
}

func (s *fakeSource) Fetch(context.Context) (fng.Reading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return fng.Reading{}, err
	}
	v := s.values[0]
	if len(s.values) > 1 {
		s.values = s.values[1:]
	}
	return fng.Reading{Value: v, Time: time.Date(2026, 9, 24, 0, 20, 0, 0, time.UTC)}, nil
}

func (s *fakeSource) setValue(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = []float64{v}
}

func (s *fakeSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type fakeNotifier struct {
	mu   sync.Mutex
	sent []string
	err  error
}

func (n *fakeNotifier) Send(_ context.Context, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.err != nil {
		return n.err
	}
	n.sent = append(n.sent, text)
	return nil
}

func (n *fakeNotifier) messages() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.sent...)
}

// clock is a manually advanced time source.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

type env struct {
	source   *fakeSource
	notifier *fakeNotifier
	store    *dedup.MemoryStore
	clock    *clock
	bot      *Bot
}

func newEnv(rules alert.Rules, values ...float64) *env {
	e := &env{
		source:   &fakeSource{values: values},
		notifier: &fakeNotifier{},
		store:    dedup.NewMemoryStore(),
		clock:    &clock{now: time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)},
	}
	e.bot = New(e.source, e.notifier, rules, dedup.New(e.store, 48*time.Hour), Options{
		Interval: 4 * time.Hour,
		Logger:   quietLogger(),
		Now:      e.clock.Now,
	})
	return e
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (e *env) check(t *testing.T) {
	t.Helper()
	if err := e.bot.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func (e *env) wantMessages(t *testing.T, n int) {
	t.Helper()
	if got := len(e.notifier.messages()); got != n {
		t.Fatalf("sent %d notifications, want %d", got, n)
	}
}

var defaultRules = alert.Rules{Fear: []float64{25}, Greed: []float64{75}}

func TestCheckNotifiesWhenKeyLevelReached(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value float64
		side  alert.Side
		level float64
		want  string
	}{
		{"fear", 18, alert.SideFear, 25, "страха: ≤ 25"},
		{"greed", 80, alert.SideGreed, 75, "жадности: ≥ 75"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(defaultRules, tt.value)
			e.check(t)
			e.wantMessages(t, 1)
			if msg := e.notifier.messages()[0]; !strings.Contains(msg, tt.want) {
				t.Errorf("message %q does not contain %q", msg, tt.want)
			}
			rec, ok := e.store.Get(string(tt.side))
			if !ok || rec.Level != tt.level || rec.Value != tt.value || !rec.SentAt.Equal(e.clock.now) {
				t.Errorf("dedup record = %+v (found %v)", rec, ok)
			}
		})
	}
}

func TestCheckStaysSilentInNeutralZone(t *testing.T) {
	e := newEnv(defaultRules, 50)
	e.check(t)
	e.wantMessages(t, 0)
}

func TestCheckRespectsCooldown(t *testing.T) {
	e := newEnv(defaultRules, 18)
	e.check(t)
	e.wantMessages(t, 1)

	// Polls every 4 hours during the next two days stay silent.
	for elapsed := 4 * time.Hour; elapsed < 48*time.Hour; elapsed += 4 * time.Hour {
		e.clock.advance(4 * time.Hour)
		e.source.setValue(20)
		e.check(t)
	}
	e.wantMessages(t, 1)

	e.clock.advance(4 * time.Hour) // 48h since the notification
	e.check(t)
	e.wantMessages(t, 2)
}

func TestCheckFearAndGreedAreIndependent(t *testing.T) {
	e := newEnv(defaultRules, 18)
	e.check(t)
	e.clock.advance(4 * time.Hour)
	e.source.setValue(80) // a sharp reversal is still reported
	e.check(t)
	e.wantMessages(t, 2)
}

func TestCheckReportsEscalationDuringCooldown(t *testing.T) {
	e := newEnv(alert.Rules{Fear: []float64{25, 10}}, 20)
	e.check(t) // level 25
	e.clock.advance(4 * time.Hour)
	e.source.setValue(8)
	e.check(t) // level 10: deeper, reported
	e.clock.advance(4 * time.Hour)
	e.source.setValue(20)
	e.check(t) // back to level 25: suppressed
	e.wantMessages(t, 2)
	if msg := e.notifier.messages()[1]; !strings.Contains(msg, "≤ 10") {
		t.Errorf("second message %q must mention level 10", msg)
	}
}

func TestCheckRetriesFailedNotification(t *testing.T) {
	e := newEnv(defaultRules, 18)
	e.notifier.err = errors.New("telegram is down")
	if err := e.bot.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "telegram is down") {
		t.Fatalf("Check error = %v, want the send error", err)
	}
	if _, ok := e.store.Get(string(alert.SideFear)); ok {
		t.Fatal("a failed notification must not start the cooldown")
	}

	e.notifier.err = nil
	e.clock.advance(5 * time.Minute)
	e.check(t)
	e.wantMessages(t, 1)
}

func TestCheckFetchError(t *testing.T) {
	e := newEnv(defaultRules, 18)
	e.source.errs = []error{errors.New("coinglass is down")}
	err := e.bot.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "coinglass is down") {
		t.Fatalf("Check error = %v, want the fetch error", err)
	}
	e.wantMessages(t, 0)
}

func TestCheckReportsStateSaveError(t *testing.T) {
	e := newEnv(defaultRules, 18)
	e.bot.dedup = dedup.New(failingStore{}, time.Hour)
	err := e.bot.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Check error = %v, want the store error", err)
	}
	e.wantMessages(t, 1)
}

type failingStore struct{}

func (failingStore) Get(string) (dedup.Record, bool) { return dedup.Record{}, false }
func (failingStore) Put(string, dedup.Record) error  { return errors.New("disk full") }

func TestRunChecksPeriodicallyUntilCancelled(t *testing.T) {
	source := &fakeSource{values: []float64{50}}
	b := New(source, &fakeNotifier{}, defaultRules, dedup.New(dedup.NewMemoryStore(), time.Hour), Options{
		Interval: 10 * time.Millisecond,
		Logger:   quietLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()

	waitFor(t, func() bool { return source.callCount() >= 3 })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestRunRetriesFailedCheckSooner(t *testing.T) {
	source := &fakeSource{values: []float64{18}, errs: []error{errors.New("temporary failure")}}
	notifier := &fakeNotifier{}
	b := New(source, notifier, defaultRules, dedup.New(dedup.NewMemoryStore(), time.Hour), Options{
		Interval:   time.Hour, // a regular check would not happen during the test
		RetryDelay: 10 * time.Millisecond,
		Logger:     quietLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	waitFor(t, func() bool { return len(notifier.messages()) == 1 })
	if got := source.callCount(); got != 2 {
		t.Errorf("source called %d times, want 2 (failure + retry)", got)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	b := New(&fakeSource{}, &fakeNotifier{}, defaultRules, nil, Options{})
	if b.interval != DefaultInterval || b.retryDelay != DefaultRetryDelay || b.log == nil || b.now == nil {
		t.Errorf("defaults not applied: interval %s, retry %s", b.interval, b.retryDelay)
	}
	b = New(&fakeSource{}, &fakeNotifier{}, defaultRules, nil, Options{Interval: time.Minute})
	if b.retryDelay != time.Minute {
		t.Errorf("retry delay %s must be capped by the interval", b.retryDelay)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}
