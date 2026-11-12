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
	return fng.Reading{Value: v, Zone: fng.Classify(v), Time: time.Date(2026, 9, 24, 0, 20, 0, 0, time.UTC)}, nil
}

var testProvider = fng.Provider{Name: "Test", URL: "https://example.com/fng"}

func (s *fakeSource) Provider() fng.Provider { return testProvider }

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
	e.bot = New(cryptoIndex(e.source, rules), e.notifier, dedup.New(e.store, 48*time.Hour), Options{
		Interval: 4 * time.Hour,
		Logger:   quietLogger(),
		Now:      e.clock.Now,
	})
	return e
}

// cryptoIndex is the single crypto index read from source.
func cryptoIndex(source Source, rules alert.Rules) []Index {
	return []Index{{Market: fng.Crypto, Source: source, Rules: rules}}
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
			msg := e.notifier.messages()[0]
			for _, want := range []string{tt.want, `Данные <a href="https://example.com/fng">Test</a>`} {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
			rec, ok := e.store.Get(dedup.Key(fng.Crypto, tt.side))
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
	if _, ok := e.store.Get(dedup.Key(fng.Crypto, alert.SideFear)); ok {
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
	b := New(cryptoIndex(source, defaultRules), &fakeNotifier{}, dedup.New(dedup.NewMemoryStore(), time.Hour), Options{
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
	b := New(cryptoIndex(source, defaultRules), notifier, dedup.New(dedup.NewMemoryStore(), time.Hour), Options{
		Interval:   time.Hour, // a regular check would not happen during the test
		RetryDelay: 10 * time.Millisecond,
		Logger:     quietLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	waitFor(t, func() bool { return len(notifier.messages()) == 2 }) // startup + alert after the retry
	if got := source.callCount(); got != 2 {
		t.Errorf("source called %d times, want 2 (failure + retry)", got)
	}
}

// runUntilMessages runs the bot until it has sent n messages.
func (e *env) runUntilMessages(t *testing.T, n int) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.bot.Run(ctx)
		close(done)
	}()
	waitFor(t, func() bool { return len(e.notifier.messages()) >= n })
	cancel()
	<-done
	return e.notifier.messages()
}

func TestRunAnnouncesStartWithCurrentValue(t *testing.T) {
	e := newEnv(defaultRules, 50)
	msgs := e.runUntilMessages(t, 1)
	if len(msgs) != 1 {
		t.Fatalf("sent %q, want only the startup message", msgs)
	}
	for _, want := range []string{
		"бот запущен",
		"🪙 <b>Крипто: 50</b> (Neutral)",
		`Данные <a href="https://example.com/fng">Test</a> на 24.09.2026 00:20 UTC`,
		"Пороги: страх ≤ 25 · жадность ≥ 75",
		"Проверка индекса: с интервалом 4 часа, следующая 24.09.2026 12:00 UTC", // the clock is at 08:00
		"уведомлениями: 2 дня",
	} {
		if !strings.Contains(msgs[0], want) {
			t.Errorf("startup message %q does not contain %q", msgs[0], want)
		}
	}
}

func TestRunAnnouncesStartBeforeAlert(t *testing.T) {
	e := newEnv(defaultRules, 18)
	msgs := e.runUntilMessages(t, 2)
	if !strings.Contains(msgs[0], "Крипто: 18</b>") || !strings.Contains(msgs[1], "Крипто — Fear &amp; Greed Index: 18") {
		t.Fatalf("want the startup message and then the alert, got %q", msgs)
	}
	if got := e.source.callCount(); got != 1 {
		t.Errorf("source called %d times, want 1: the startup message reuses the first check", got)
	}
}

func TestRunAnnouncesFetchError(t *testing.T) {
	e := newEnv(defaultRules, 18)
	e.source.errs = []error{errors.New("unexpected HTTP status 503 Service Unavailable")}
	msgs := e.runUntilMessages(t, 1)
	for _, want := range []string{
		"🪙 <b>Крипто</b>\n⚠️ " + `Не удалось получить индекс с <a href="https://example.com/fng">Test</a>: unexpected HTTP status 503`,
		"следующая 24.09.2026 08:05 UTC", // retried after the default 5 minutes
	} {
		if !strings.Contains(msgs[0], want) {
			t.Errorf("startup message %q does not contain %q", msgs[0], want)
		}
	}
}

func TestRunKeepsCheckingWhenStartupMessageFails(t *testing.T) {
	source := &fakeSource{values: []float64{50}}
	notifier := &fakeNotifier{err: errors.New("telegram is down")}
	b := New(cryptoIndex(source, defaultRules), notifier, dedup.New(dedup.NewMemoryStore(), time.Hour), Options{
		Interval: 10 * time.Millisecond,
		Logger:   quietLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	waitFor(t, func() bool { return source.callCount() >= 3 })
}

func TestNewAppliesDefaults(t *testing.T) {
	b := New(cryptoIndex(&fakeSource{}, defaultRules), &fakeNotifier{}, nil, Options{})
	if b.interval != DefaultInterval || b.retryDelay != DefaultRetryDelay || b.log == nil || b.now == nil {
		t.Errorf("defaults not applied: interval %s, retry %s", b.interval, b.retryDelay)
	}
	b = New(cryptoIndex(&fakeSource{}, defaultRules), &fakeNotifier{}, nil, Options{Interval: time.Minute})
	if b.retryDelay != time.Minute {
		t.Errorf("retry delay %s must be capped by the interval", b.retryDelay)
	}
}

// twoIndexes is a bot watching a crypto and a stocks index with the same levels.
type twoIndexes struct {
	crypto, stocks *fakeSource
	notifier       *fakeNotifier
	store          *dedup.MemoryStore
	bot            *Bot
}

func newTwoIndexes(crypto, stocks *fakeSource, opts Options) *twoIndexes {
	e := &twoIndexes{crypto: crypto, stocks: stocks, notifier: &fakeNotifier{}, store: dedup.NewMemoryStore()}
	opts.Logger = quietLogger()
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC) }
	}
	e.bot = New([]Index{
		{Market: fng.Crypto, Source: crypto, Rules: defaultRules},
		{Market: fng.Stocks, Source: stocks, Rules: alert.Rules{Fear: []float64{20}, Greed: []float64{80}}},
	}, e.notifier, dedup.New(e.store, 48*time.Hour), opts)
	return e
}

func TestCheckNotifiesEachIndexSeparately(t *testing.T) {
	e := newTwoIndexes(&fakeSource{values: []float64{18}}, &fakeSource{values: []float64{15}}, Options{})
	if err := e.bot.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	msgs := e.notifier.messages()
	if len(msgs) != 2 || !strings.Contains(msgs[0], "Крипто — Fear &amp; Greed Index: 18") || !strings.Contains(msgs[0], "≤ 25") ||
		!strings.Contains(msgs[1], "Акции США — Fear &amp; Greed Index: 15") || !strings.Contains(msgs[1], "≤ 20") {
		t.Fatalf("want a crypto and a stocks alert with their own levels, got %q", msgs)
	}
	for _, key := range []string{"crypto.fear", "stocks.fear"} {
		if _, ok := e.store.Get(key); !ok {
			t.Errorf("no dedup record %q", key)
		}
	}

	// Both are within their cooldowns now.
	if err := e.bot.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(e.notifier.messages()); got != 2 {
		t.Errorf("sent %d notifications, want still 2", got)
	}
}

func TestCheckContinuesAfterFailedIndex(t *testing.T) {
	crypto := &fakeSource{values: []float64{50}, errs: []error{errors.New("alternative.me is down")}}
	e := newTwoIndexes(crypto, &fakeSource{values: []float64{85}}, Options{})
	err := e.bot.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "crypto index: fetch index: alternative.me is down") {
		t.Fatalf("Check error = %v, want the crypto fetch error", err)
	}
	if msgs := e.notifier.messages(); len(msgs) != 1 || !strings.Contains(msgs[0], "Акции США — Fear &amp; Greed Index: 85") {
		t.Errorf("the stocks alert must still be sent, got %q", msgs)
	}
}

func TestRunAnnouncesBothIndexes(t *testing.T) {
	stocks := &fakeSource{values: []float64{50}, errs: []error{errors.New("unexpected HTTP status 418")}}
	e := newTwoIndexes(&fakeSource{values: []float64{60}}, stocks, Options{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.bot.Run(ctx)

	waitFor(t, func() bool { return len(e.notifier.messages()) == 1 })
	msg := e.notifier.messages()[0]
	for _, want := range []string{
		"🪙 <b>Крипто: 60</b> (Neutral)",
		"Пороги: страх ≤ 25 · жадность ≥ 75",
		"📈 <b>Акции США</b>\n⚠️ Не удалось получить индекс с ",
		"Пороги: страх ≤ 20 · жадность ≥ 80",
		"Проверка индексов: с интервалом 1 час, следующая 24.09.2026 08:05 UTC", // the stocks index is retried soon
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup message %q does not contain %q", msg, want)
		}
	}
	if strings.Index(msg, "Крипто") > strings.Index(msg, "Акции США") {
		t.Errorf("crypto must come first: %q", msg)
	}
}

func TestRunRetriesOnlyTheFailedIndex(t *testing.T) {
	crypto := &fakeSource{values: []float64{50}}
	stocks := &fakeSource{values: []float64{50}, errs: []error{errors.New("down"), errors.New("down"), errors.New("down")}}
	e := newTwoIndexes(crypto, stocks, Options{Interval: time.Hour, RetryDelay: time.Millisecond, Now: time.Now})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.bot.Run(ctx)
		close(done)
	}()

	waitFor(t, func() bool { return stocks.callCount() >= 4 }) // the startup check and three retries
	cancel()
	<-done
	if got := crypto.callCount(); got != 1 {
		t.Errorf("crypto source called %d times, want 1: its next check is an hour later", got)
	}
}

func TestFetch(t *testing.T) {
	statuses := Fetch(context.Background(), []Index{
		{Market: fng.Crypto, Source: &fakeSource{values: []float64{30}}, Rules: defaultRules},
		{Market: fng.Stocks, Source: &fakeSource{errs: []error{errors.New("down")}}},
	})
	if len(statuses) != 2 {
		t.Fatalf("got %d statuses", len(statuses))
	}
	if s := statuses[0]; s.Market != fng.Crypto || s.FetchErr != nil || s.Reading.Value != 30 || s.Provider != testProvider || len(s.Rules.Fear) != 1 {
		t.Errorf("crypto status = %+v", s)
	}
	if s := statuses[1]; s.Market != fng.Stocks || s.FetchErr == nil {
		t.Errorf("stocks status = %+v, want the fetch error", s)
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
