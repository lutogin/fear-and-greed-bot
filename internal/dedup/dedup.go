// Package dedup prevents sending the same notification too often.
package dedup

import (
	"time"

	"fear-and-greed-bot/internal/alert"
	"fear-and-greed-bot/internal/fng"
)

// Deduplicator applies a cooldown to alerts per index and side. Fear and greed
// are tracked independently, so a sharp reversal of the market is still
// reported, and the indexes of different markets do not suppress each other.
type Deduplicator struct {
	store    Store
	cooldown time.Duration
}

func New(store Store, cooldown time.Duration) *Deduplicator {
	return &Deduplicator{store: store, cooldown: cooldown}
}

// Cooldown is the minimum time between notifications about the same side of an index.
func (d *Deduplicator) Cooldown() time.Duration { return d.cooldown }

// Allow reports whether a notification for alert a of the market's index may be
// sent at now, and returns the previous record of the same side, if any. The
// notification is suppressed when the side was notified less than cooldown ago
// at the same or a more extreme level; reaching a more extreme level than the
// last notified one is always reported.
func (d *Deduplicator) Allow(m fng.Market, a alert.Alert, now time.Time) (bool, Record) {
	last, ok := d.store.Get(Key(m, a.Side))
	if !ok {
		return true, Record{}
	}
	if now.Sub(last.SentAt) >= d.cooldown || alert.MoreExtreme(a.Side, a.Level, last.Level) {
		return true, last
	}
	return false, last
}

// Mark records that a notification for alert a of the market's index was sent at now.
func (d *Deduplicator) Mark(m fng.Market, a alert.Alert, now time.Time) error {
	return d.store.Put(Key(m, a.Side), Record{Level: a.Level, Value: a.Value, SentAt: now.UTC()})
}

// Key is the store key of a side of the market's index, e.g. "stocks.fear".
func Key(m fng.Market, side alert.Side) string {
	return string(m) + "." + string(side)
}
