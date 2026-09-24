package alert

import (
	"strings"
	"testing"
)

func TestEvaluate(t *testing.T) {
	rules := Rules{Fear: []float64{25, 10}, Greed: []float64{75, 90}}
	tests := []struct {
		name  string
		value float64
		want  Alert
		ok    bool
	}{
		{name: "neutral", value: 50},
		{name: "just above the fear level", value: 25.5},
		{name: "fear level is inclusive", value: 25, want: Alert{SideFear, 25, 25}, ok: true},
		{name: "fear", value: 18, want: Alert{SideFear, 25, 18}, ok: true},
		{name: "most extreme fear level wins", value: 7, want: Alert{SideFear, 10, 7}, ok: true},
		{name: "zero", value: 0, want: Alert{SideFear, 10, 0}, ok: true},
		{name: "just below the greed level", value: 74.9},
		{name: "greed level is inclusive", value: 75, want: Alert{SideGreed, 75, 75}, ok: true},
		{name: "most extreme greed level wins", value: 95, want: Alert{SideGreed, 90, 95}, ok: true},
		{name: "hundred", value: 100, want: Alert{SideGreed, 90, 100}, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rules.Evaluate(tt.value)
			if ok != tt.ok || got != tt.want {
				t.Errorf("Evaluate(%v) = %+v, %v; want %+v, %v", tt.value, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestEvaluateOneSide(t *testing.T) {
	rules := Rules{Greed: []float64{80}}
	if a, ok := rules.Evaluate(3); ok {
		t.Errorf("no fear levels configured, got alert %+v", a)
	}
	if a, ok := rules.Evaluate(85); !ok || a.Side != SideGreed {
		t.Errorf("Evaluate(85) = %+v, %v; want a greed alert", a, ok)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		rules   Rules
		wantErr string
	}{
		{name: "valid", rules: Rules{Fear: []float64{25, 10}, Greed: []float64{75}}},
		{name: "fear only", rules: Rules{Fear: []float64{20}}},
		{name: "empty", rules: Rules{}, wantErr: "at least one"},
		{name: "below zero", rules: Rules{Fear: []float64{-1}}, wantErr: "outside of 0..100"},
		{name: "above hundred", rules: Rules{Greed: []float64{101}}, wantErr: "outside of 0..100"},
		{name: "overlapping zones", rules: Rules{Fear: []float64{60}, Greed: []float64{50}}, wantErr: "lower than greed"},
		{name: "touching zones", rules: Rules{Fear: []float64{50}, Greed: []float64{50}}, wantErr: "lower than greed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rules.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestMoreExtreme(t *testing.T) {
	if !MoreExtreme(SideFear, 10, 25) || MoreExtreme(SideFear, 25, 10) || MoreExtreme(SideFear, 25, 25) {
		t.Error("for fear a lower level must be more extreme")
	}
	if !MoreExtreme(SideGreed, 90, 75) || MoreExtreme(SideGreed, 75, 90) || MoreExtreme(SideGreed, 75, 75) {
		t.Error("for greed a higher level must be more extreme")
	}
}
