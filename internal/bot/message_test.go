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

func TestStartMessage(t *testing.T) {
	msg := StartMessage(alert.Rules{Fear: []float64{20}, Greed: []float64{80}}, 4*time.Hour, 48*time.Hour)
	want := "🚀 <b>Fear &amp; Greed бот запущен</b>\n\n" +
		"Пороги:\n" +
		"😱 страх: ≤ 20\n" +
		"🤑 жадность: ≥ 80\n\n" +
		"Проверка индекса: сразу после запуска, затем с интервалом 4 часа\n" +
		"Пауза между повторными уведомлениями: 2 дня\n" +
		coinglass.PageURL
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestStartMessageLevels(t *testing.T) {
	fear, greed := []float64{10, 25}, []float64{90, 75}
	msg := StartMessage(alert.Rules{Fear: fear, Greed: greed}, 90*time.Minute, 0)
	for _, want := range []string{
		"страх: ≤ 25, ≤ 10", // from the first reached level to the deepest
		"жадность: ≥ 75, ≥ 90",
		"с интервалом 1 час 30 минут",
		"повторными уведомлениями: нет",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not contain %q", msg, want)
		}
	}
	if fear[0] != 10 || greed[0] != 90 {
		t.Errorf("configured levels were reordered: %v %v", fear, greed)
	}

	msg = StartMessage(alert.Rules{Greed: []float64{80}}, time.Hour, time.Hour)
	if !strings.Contains(msg, "страх: не отслеживается") {
		t.Errorf("message %q must say that fear is not tracked", msg)
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{time.Minute, "1 минута"},
		{2 * time.Minute, "2 минуты"},
		{5 * time.Minute, "5 минут"},
		{21 * time.Minute, "21 минута"},
		{time.Hour, "1 час"},
		{4 * time.Hour, "4 часа"},
		{11 * time.Hour, "11 часов"},
		{12 * time.Hour, "12 часов"},
		{22 * time.Hour, "22 часа"},
		{24 * time.Hour, "1 день"},
		{48 * time.Hour, "2 дня"},
		{5 * 24 * time.Hour, "5 дней"},
		{14 * 24 * time.Hour, "14 дней"},
		{111 * 24 * time.Hour, "111 дней"},
		{121 * 24 * time.Hour, "121 день"},
		{36 * time.Hour, "1 день 12 часов"},
		{90 * time.Second, "1 минута 30 секунд"},
		{0, "0 секунд"},
	}
	for _, tt := range tests {
		if got := humanDuration(tt.in); got != tt.want {
			t.Errorf("humanDuration(%s) = %q, want %q", tt.in, got, tt.want)
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
