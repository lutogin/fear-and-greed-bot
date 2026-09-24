package dedup

import (
	"testing"
	"time"

	"fear-and-greed-bot/internal/alert"
)

var (
	t0      = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fear25  = alert.Alert{Side: alert.SideFear, Level: 25, Value: 22}
	fear10  = alert.Alert{Side: alert.SideFear, Level: 10, Value: 8}
	greed75 = alert.Alert{Side: alert.SideGreed, Level: 75, Value: 77}
	greed90 = alert.Alert{Side: alert.SideGreed, Level: 90, Value: 91}
)

func TestDeduplicatorAllow(t *testing.T) {
	const cooldown = 48 * time.Hour
	tests := []struct {
		name  string
		sent  []alert.Alert // notified at t0
		next  alert.Alert
		after time.Duration // since t0
		want  bool
	}{
		{name: "first notification", next: fear25, want: true},
		{name: "same level within cooldown", sent: []alert.Alert{fear25}, next: fear25, after: 4 * time.Hour, want: false},
		{name: "just before the cooldown ends", sent: []alert.Alert{fear25}, next: fear25, after: cooldown - time.Second, want: false},
		{name: "cooldown elapsed", sent: []alert.Alert{fear25}, next: fear25, after: cooldown, want: true},
		{name: "deeper fear within cooldown", sent: []alert.Alert{fear25}, next: fear10, after: time.Hour, want: true},
		{name: "shallower fear within cooldown", sent: []alert.Alert{fear10}, next: fear25, after: time.Hour, want: false},
		{name: "higher greed within cooldown", sent: []alert.Alert{greed75}, next: greed90, after: time.Hour, want: true},
		{name: "lower greed within cooldown", sent: []alert.Alert{greed90}, next: greed75, after: time.Hour, want: false},
		{name: "fear does not block greed", sent: []alert.Alert{fear25}, next: greed75, after: time.Hour, want: true},
		{name: "greed does not block fear", sent: []alert.Alert{greed75}, next: fear25, after: time.Hour, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New(NewMemoryStore(), cooldown)
			for _, a := range tt.sent {
				if err := d.Mark(a, t0); err != nil {
					t.Fatal(err)
				}
			}
			if got, _ := d.Allow(tt.next, t0.Add(tt.after)); got != tt.want {
				t.Errorf("Allow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeduplicatorMarkStoresRecord(t *testing.T) {
	store := NewMemoryStore()
	d := New(store, time.Hour)
	moscow := time.FixedZone("MSK", 3*60*60)
	if err := d.Mark(fear25, t0.In(moscow)); err != nil {
		t.Fatal(err)
	}
	got, ok := store.Get(string(alert.SideFear))
	want := Record{Level: 25, Value: 22, SentAt: t0}
	if !ok || got != want {
		t.Fatalf("stored %+v (found %v), want %+v", got, ok, want)
	}
	if _, last := d.Allow(fear25, t0); last != want {
		t.Errorf("Allow returned last record %+v, want %+v", last, want)
	}
}

func TestZeroCooldownNeverSuppresses(t *testing.T) {
	d := New(NewMemoryStore(), 0)
	if err := d.Mark(fear25, t0); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.Allow(fear25, t0); !ok {
		t.Error("with zero cooldown every notification must be allowed")
	}
}
