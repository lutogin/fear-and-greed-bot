package bot

import (
	"strings"
	"testing"
	"time"

	"fear-and-greed-bot/internal/alert"
	"fear-and-greed-bot/internal/coinglass"
	"fear-and-greed-bot/internal/fng"
)

var reading = fng.Reading{Value: 18, Time: time.Date(2026, 9, 24, 0, 20, 1, 0, time.UTC), Price: 84400.7}

func TestMessageFear(t *testing.T) {
	msg := Message(reading, alert.Alert{Side: alert.SideFear, Level: 25, Value: 18})
	want := "😱 <b>Fear &amp; Greed Index: 18</b> (Extreme Fear)\n" +
		"Достигнут ключевой уровень страха: ≤ 25\n" +
		"BTC: $84,401\n" +
		"Данные на 24.09.2026 00:20 UTC\n" +
		coinglass.PageURL
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestMessageGreed(t *testing.T) {
	r := fng.Reading{Value: 77.5}
	msg := Message(r, alert.Alert{Side: alert.SideGreed, Level: 75, Value: 77.5})
	for _, want := range []string{"🤑", "Index: 77.5</b> (Greed)", "жадности: ≥ 75"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not contain %q", msg, want)
		}
	}
	// Unknown price and time are omitted.
	for _, unwanted := range []string{"BTC:", "Данные на"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("message %q must not contain %q", msg, unwanted)
		}
	}
}

func TestTestMessage(t *testing.T) {
	msg := TestMessage(reading)
	for _, want := range []string{"✅", "Fear &amp; Greed Index: 18</b> (Extreme Fear)", "BTC: $84,401", coinglass.PageURL} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not contain %q", msg, want)
		}
	}
}

func TestNumber(t *testing.T) {
	for in, want := range map[float64]string{72: "72", 72.5: "72.5", 33.333: "33.33", 0: "0", 100: "100"} {
		if got := number(in); got != want {
			t.Errorf("number(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestUSD(t *testing.T) {
	for in, want := range map[float64]string{
		0: "$0", 999.4: "$999", 999.5: "$1,000", 84400.7: "$84,401", 1234567: "$1,234,567",
	} {
		if got := usd(in); got != want {
			t.Errorf("usd(%v) = %q, want %q", in, got, want)
		}
	}
}
