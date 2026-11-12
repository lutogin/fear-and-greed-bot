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
	msg := Message(fng.Crypto, altme, reading, alert.Alert{Side: alert.SideFear, Level: 25, Value: 18})
	want := "😱 <b>Крипто — Fear &amp; Greed Index: 18</b> (Extreme Fear)\n" +
		"Достигнут ключевой уровень страха: ≤ 25\n" +
		"Данные " + credited + " на 25.09.2026 00:00 UTC"
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestMessageStocksGreed(t *testing.T) {
	r := fng.Reading{Value: 78, Zone: fng.ExtremeGreed, Time: time.Date(2026, 11, 12, 21, 43, 31, 0, time.UTC)}
	msg := Message(fng.Stocks, cnnProvider, r, alert.Alert{Side: alert.SideGreed, Level: 75, Value: 78})
	want := "🤑 <b>Акции США — Fear &amp; Greed Index: 78</b> (Extreme Greed)\n" +
		"Достигнут ключевой уровень жадности: ≥ 75\n" +
		"Данные " + cnnCredited + " на 12.11.2026 21:43 UTC"
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestMessageWithPrice(t *testing.T) {
	coinglass := fng.Provider{Name: "CoinGlass", URL: "https://www.coinglass.com/pro/i/FearGreedIndex"}
	r := fng.Reading{Value: 72, Zone: fng.Greed, Time: time.Date(2026, 9, 24, 0, 20, 1, 0, time.UTC), Price: 84400.7}
	msg := Message(fng.Crypto, coinglass, r, alert.Alert{Side: alert.SideGreed, Level: 70, Value: 72})
	want := "BTC: $84,401\n" +
		`Данные <a href="https://www.coinglass.com/pro/i/FearGreedIndex">CoinGlass</a> на 24.09.2026 00:20 UTC`
	if !strings.HasSuffix(msg, want) {
		t.Errorf("message %q must end with %q", msg, want)
	}
}

func TestMessageWithUnknownDetails(t *testing.T) {
	msg := Message("bonds & <x>", altme, fng.Reading{Value: 77.5}, alert.Alert{Side: alert.SideGreed, Level: 75, Value: 77.5})
	want := "🤑 <b>bonds &amp; &lt;x&gt; — Fear &amp; Greed Index: 77.5</b>\n" +
		"Достигнут ключевой уровень жадности: ≥ 75\n" +
		"Данные " + credited // the source is credited even without a date
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestMessageEscapesProvider(t *testing.T) {
	p := fng.Provider{Name: "A&B <x>", URL: `https://example.com/?a=1&b="2"`}
	msg := Message(fng.Crypto, p, reading, alert.Alert{Side: alert.SideFear, Level: 25, Value: 18})
	if want := `<a href="https://example.com/?a=1&amp;b=&#34;2&#34;">A&amp;B &lt;x&gt;</a>`; !strings.Contains(msg, want) {
		t.Errorf("message %q does not contain %q", msg, want)
	}
}

var (
	cnnProvider = fng.Provider{Name: "CNN", URL: "https://edition.cnn.com/markets/fear-and-greed"}
	cnnCredited = `<a href="https://edition.cnn.com/markets/fear-and-greed">CNN</a>`
	cryptoNow   = Status{
		Market:   fng.Crypto,
		Provider: altme,
		Rules:    alert.Rules{Fear: []float64{20}, Greed: []float64{80}},
		Reading:  fng.Reading{Value: 71, Zone: fng.Greed, Time: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)},
	}
	stocksNow = Status{
		Market:   fng.Stocks,
		Provider: cnnProvider,
		Rules:    alert.Rules{Fear: []float64{25}, Greed: []float64{75}},
		Reading:  fng.Reading{Value: 40, Zone: fng.Fear, Time: time.Date(2026, 9, 24, 20, 0, 0, 0, time.UTC)},
	}
)

func TestTestMessage(t *testing.T) {
	msg := TestMessage([]Status{cryptoNow, stocksNow})
	want := "✅ Бот подключён\n\n" +
		"🪙 <b>Крипто: 71</b> (Greed)\n" +
		"Данные " + credited + " на 25.09.2026 00:00 UTC\n\n" +
		"📈 <b>Акции США: 40</b> (Fear)\n" +
		"Данные " + cnnCredited + " на 24.09.2026 20:00 UTC"
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestTestMessageFetchError(t *testing.T) {
	failed := stocksNow
	failed.Reading, failed.FetchErr = fng.Reading{}, errors.New("unexpected HTTP status 418")
	msg := TestMessage([]Status{cryptoNow, failed})
	if want := "📈 <b>Акции США</b>\n⚠️ Не удалось получить индекс с " + cnnCredited + ": unexpected HTTP status 418"; !strings.HasSuffix(msg, want) {
		t.Errorf("message %q must end with %q", msg, want)
	}
	if !strings.Contains(msg, "Крипто: 71") {
		t.Errorf("the readable index must still be shown: %q", msg)
	}
}

var startInfo = StartInfo{
	Indexes:   []Status{cryptoNow, stocksNow},
	Interval:  4 * time.Hour,
	Cooldown:  48 * time.Hour,
	NextCheck: time.Date(2026, 9, 25, 14, 30, 0, 0, time.FixedZone("CEST", 2*60*60)),
}

func TestStartMessage(t *testing.T) {
	msg := StartMessage(startInfo)
	want := "🚀 <b>Fear &amp; Greed бот запущен</b>\n\n" +
		"🪙 <b>Крипто: 71</b> (Greed)\n" +
		"Данные " + credited + " на 25.09.2026 00:00 UTC\n" +
		"Пороги: страх ≤ 20 · жадность ≥ 80\n\n" +
		"📈 <b>Акции США: 40</b> (Fear)\n" +
		"Данные " + cnnCredited + " на 24.09.2026 20:00 UTC\n" +
		"Пороги: страх ≤ 25 · жадность ≥ 75\n\n" +
		"Проверка индексов: с интервалом 4 часа, следующая 25.09.2026 12:30 UTC\n" +
		"Пауза между повторными уведомлениями: 2 дня"
	if msg != want {
		t.Errorf("message:\n%s\nwant:\n%s", msg, want)
	}
}

func TestStartMessageSingleIndex(t *testing.T) {
	info := startInfo
	info.Indexes = []Status{stocksNow}
	msg := StartMessage(info)
	if strings.Contains(msg, "Крипто") || !strings.Contains(msg, "Акции США: 40") || !strings.Contains(msg, "Проверка индекса: ") {
		t.Errorf("message %q must report only the stocks index", msg)
	}
}

func TestStartMessageFetchError(t *testing.T) {
	failed := cryptoNow
	failed.Reading = fng.Reading{}
	failed.FetchErr = errors.New(`invalid index value "abc" <b>` + strings.Repeat("x", 400))
	info := startInfo
	info.Indexes = []Status{failed, stocksNow}
	msg := StartMessage(info)
	if want := "🪙 <b>Крипто</b>\n⚠️ Не удалось получить индекс с " + credited + `: invalid index value &#34;abc&#34; &lt;b&gt;xxx`; !strings.Contains(msg, want) {
		t.Errorf("message %q must show the escaped error, want %q", msg, want)
	}
	if strings.Contains(msg, strings.Repeat("x", 300)) || !strings.Contains(msg, "x…") {
		t.Errorf("a long error must be truncated: %q", msg)
	}
	if strings.Contains(msg, "Данные "+credited) {
		t.Errorf("message %q must not credit the failed source with data", msg)
	}
	for _, want := range []string{"Пороги: страх ≤ 20 · жадность ≥ 80", "Акции США: 40", "следующая 25.09.2026 12:30 UTC"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q must still contain %q", msg, want)
		}
	}

	failed.Provider = fng.Provider{}
	info.Indexes = []Status{failed}
	if msg := StartMessage(info); !strings.Contains(msg, "⚠️ Не удалось получить индекс: invalid index value") {
		t.Errorf("without a provider the error must be shown as is: %q", msg)
	}
}

func TestStartMessageLevels(t *testing.T) {
	fear, greed := []float64{10, 25}, []float64{90, 75}
	msg := StartMessage(StartInfo{Indexes: []Status{{Market: fng.Crypto, Rules: alert.Rules{Fear: fear, Greed: greed}}}, Interval: 90 * time.Minute})
	for _, want := range []string{
		"страх ≤ 25, ≤ 10 · жадность ≥ 75, ≥ 90", // from the first reached level to the deepest
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

	msg = StartMessage(StartInfo{Indexes: []Status{{Market: fng.Stocks, Rules: alert.Rules{Greed: []float64{80}}}}, Interval: time.Hour, Cooldown: time.Hour})
	if !strings.Contains(msg, "страх не отслеживается · жадность ≥ 80") {
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
