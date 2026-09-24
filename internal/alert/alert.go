// Package alert decides whether an index value reached one of the configured key levels.
package alert

import (
	"errors"
	"fmt"
	"slices"
)

// Side is the half of the index scale a key level belongs to.
type Side string

const (
	SideFear  Side = "fear"
	SideGreed Side = "greed"
)

// Alert is a key level reached by the index.
type Alert struct {
	Side  Side
	Level float64 // the key level that was reached
	Value float64 // the index value that reached it
}

// Rules holds the key levels. A fear level is reached when the index is at or
// below it, a greed level when the index is at or above it.
type Rules struct {
	Fear  []float64
	Greed []float64
}

// Evaluate returns the most extreme level reached by value, if any.
func (r Rules) Evaluate(value float64) (Alert, bool) {
	best, found := Alert{}, false
	for _, level := range r.Fear {
		if value <= level && (!found || level < best.Level) {
			best, found = Alert{Side: SideFear, Level: level, Value: value}, true
		}
	}
	if found {
		return best, true
	}
	for _, level := range r.Greed {
		if value >= level && (!found || level > best.Level) {
			best, found = Alert{Side: SideGreed, Level: level, Value: value}, true
		}
	}
	return best, found
}

// Validate checks that the levels are usable.
func (r Rules) Validate() error {
	var errs []error
	if len(r.Fear) == 0 && len(r.Greed) == 0 {
		errs = append(errs, errors.New("at least one fear or greed level is required"))
	}
	for _, level := range slices.Concat(r.Fear, r.Greed) {
		if level < 0 || level > 100 {
			errs = append(errs, fmt.Errorf("level %v is outside of 0..100", level))
		}
	}
	if len(r.Fear) > 0 && len(r.Greed) > 0 && slices.Max(r.Fear) >= slices.Min(r.Greed) {
		errs = append(errs, errors.New("fear levels must be lower than greed levels"))
	}
	return errors.Join(errs...)
}

// MoreExtreme reports whether level a lies deeper in the side's zone than level b.
func MoreExtreme(side Side, a, b float64) bool {
	if side == SideFear {
		return a < b
	}
	return a > b
}
