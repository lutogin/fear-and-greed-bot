package fng

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		value float64
		want  Zone
	}{
		{0, ExtremeFear},
		{20, ExtremeFear},
		{20.5, Fear},
		{40, Fear},
		{41, Neutral},
		{60, Neutral},
		{61, Greed},
		{80, Greed},
		{80.1, ExtremeGreed},
		{100, ExtremeGreed},
	}
	for _, tt := range tests {
		if got := Classify(tt.value); got != tt.want {
			t.Errorf("Classify(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
}
