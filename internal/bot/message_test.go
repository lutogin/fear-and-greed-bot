package bot

import (
	"errors"
	"strings"
	"testing"
	"time"

	"fear-and-greed-bot/internal/alert"
	"fear-and-greed-bot/internal/fng"
)

var (
	altme    = fng.Provider{Name: "alternative.me", URL: "https://alternative.me/crypto/fear-and-greed-index/"}
	credited = `<a href="https://alternative.me/crypto/fear-and-greed-index/">alternative.me</a>`
	reading  = fng.Reading{Value: 18, Zone: fng.ExtremeFear, Time: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)}
)

func TestMessageFear(t *testing.T) {
	msg := Message(altme, reading, alert.Alert{Side: alert.SideFear, Level: 25, Value: 18})
	want := "😱 <b>Fear &amp; Greed Index: 18</b> (Extreme Fear)\n" +
		"Достигнут ключевой уровень страха: ≤ 25\n" +
		"Данные " + credited + " на 25.09.2026 00:00 UTC"
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestMessageWithPrice(t *testing.T) {
	coinglass := fng.Provider{Name: "CoinGlass", URL: "https://www.coinglass.com/pro/i/FearGreedIndex"}
	r := fng.Reading{Value: 72, Zone: fng.Greed, Time: time.Date(2026, 9, 24, 0, 20, 1, 0, time.UTC), Price: 84400.7}
	msg := Message(coinglass, r, alert.Alert{Side: alert.SideGreed, Level: 70, Value: 72})
	want := "BTC: $84,401\n" +
		`Данные <a href="https://www.coinglass.com/pro/i/FearGreedIndex">CoinGlass</a> на 24.09.2026 00:20 UTC`
	if !strings.HasSuffix(msg, want) {
		t.Errorf("message %q must end with %q", msg, want)
	}
}

func TestMessageWithUnknownDetails(t *testing.T) {
	msg := Message(altme, fng.Reading{Value: 77.5}, alert.Alert{Side: alert.SideGreed, Level: 75, Value: 77.5})
	want := "🤑 <b>Fear &amp; Greed Index: 77.5</b>\n" +
		"Достигнут ключевой уровень жадности: ≥ 75\n" +
		"Данные " + credited // the source is credited even without a date
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestMessageEscapesProvider(t *testing.T) {
	p := fng.Provider{Name: "A&B <x>", URL: `https://example.com/?a=1&b="2"`}
	msg := Message(p, reading, alert.Alert{Side: alert.SideFear, Level: 25, Value: 18})
	if want := `<a href="https://example.com/?a=1&amp;b=&#34;2&#34;">A&amp;B &lt;x&gt;</a>`; !strings.Contains(msg, want) {
		t.Errorf("message %q does not contain %q", msg, want)
	}
}

func TestTestMessage(t *testing.T) {
	msg := TestMessage(altme, reading)
	want := "✅ Бот подключён. <b>Fear &amp; Greed Index: 18</b> (Extreme Fear)\n" +
		"Данные " + credited + " на 25.09.2026 00:00 UTC"
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

var startInfo = StartInfo{
	Provider:  altme,
	Reading:   fng.Reading{Value: 71, Zone: fng.Greed, Time: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)},
	Rules:     alert.Rules{Fear: []float64{20}, Greed: []float64{80}},
	Interval:  4 * time.Hour,
	Cooldown:  48 * time.Hour,
	NextCheck: time.Date(2026, 9, 25, 14, 30, 0, 0, time.FixedZone("CEST", 2*60*60)),
}

func TestStartMessage(t *testing.T) {
	msg := StartMessage(startInfo)
	want := "🚀 <b>Fear &amp; Greed бот запущен</b>\n\n" +
		"Индекс сейчас: <b>71</b> (Greed)\n" +
		"Данные " + credited + " на 25.09.2026 00:00 UTC\n\n" +
		"Пороги:\n" +
		"😱 страх: ≤ 20\n" +
		"🤑 жадность: ≥ 80\n\n" +
		"Проверка индекса: с интервалом 4 часа, следующая 25.09.2026 12:30 UTC\n" +
		"Пауза между повторными уведомлениями: 2 дня"
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestStartMessageFetchError(t *testing.T) {
	info := startInfo
	info.Reading = fng.Reading{}
	info.FetchErr = errors.New(`invalid index value "abc" <b>` + strings.Repeat("x", 400))
	msg := StartMessage(info)
	if want := "⚠️ Не удалось получить индекс с " + credited + `: invalid index value &#34;abc&#34; &lt;b&gt;xxx`; !strings.Contains(msg, want) {
		t.Errorf("message %q must show the escaped error, want %q", msg, want)
	}
	if strings.Contains(msg, strings.Repeat("x", 300)) || !strings.Contains(msg, "x…") {
		t.Errorf("a long error must be truncated: %q", msg)
	}
	for _, unwanted := range []string{"Индекс сейчас", "Данные"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("message %q must not contain %q", msg, unwanted)
		}
	}
	if !strings.Contains(msg, "страх: ≤ 20") || !strings.Contains(msg, "следующая 25.09.2026 12:30 UTC") {
		t.Errorf("levels and schedule must still be reported: %q", msg)
	}

	info.Provider = fng.Provider{}
	if msg := StartMessage(info); !strings.Contains(msg, "⚠️ Не удалось получить индекс: invalid index value") {
		t.Errorf("without a provider the error must be shown as is: %q", msg)
	}
}

func TestStartMessageLevels(t *testing.T) {
	fear, greed := []float64{10, 25}, []float64{90, 75}
	msg := StartMessage(StartInfo{Rules: alert.Rules{Fear: fear, Greed: greed}, Interval: 90 * time.Minute})
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

	msg = StartMessage(StartInfo{Rules: alert.Rules{Greed: []float64{80}}, Interval: time.Hour, Cooldown: time.Hour})
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
