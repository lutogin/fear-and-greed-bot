package bot

import (
	"fmt"
	"html"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"fear-and-greed-bot/internal/alert"
	"fear-and-greed-bot/internal/fng"
)

// Message is the Telegram notification (HTML) about a reached key level.
func Message(p fng.Provider, r fng.Reading, a alert.Alert) string {
	icon, what := "😱", "страха"
	if a.Side == alert.SideGreed {
		icon, what = "🤑", "жадности"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s <b>Fear &amp; Greed Index: %s</b>%s\n", icon, number(r.Value), zone(r))
	fmt.Fprintf(&b, "Достигнут ключевой уровень %s: %s %s\n", what, sign(a.Side), number(a.Level))
	writeReading(&b, p, r)
	return strings.TrimSuffix(b.String(), "\n")
}

// StartInfo is what the startup message reports.
type StartInfo struct {
	Provider  fng.Provider
	Reading   fng.Reading // the current index value, valid when FetchErr is nil
	FetchErr  error       // why the current value could not be read
	Rules     alert.Rules
	Interval  time.Duration
	Cooldown  time.Duration
	NextCheck time.Time
}

// StartMessage announces that the bot is running: the current index value,
// which also shows that the source is still read correctly, the key levels
// and the schedule.
func StartMessage(s StartInfo) string {
	pause := "нет"
	if s.Cooldown > 0 {
		pause = humanDuration(s.Cooldown)
	}
	var b strings.Builder
	b.WriteString("🚀 <b>Fear &amp; Greed бот запущен</b>\n\n")
	if s.FetchErr != nil {
		from := ""
		if s.Provider.Name != "" {
			from = " с " + credit(s.Provider)
		}
		fmt.Fprintf(&b, "⚠️ Не удалось получить индекс%s: %s\n\n", from, html.EscapeString(truncate(s.FetchErr.Error(), 300)))
	} else {
		fmt.Fprintf(&b, "Индекс сейчас: <b>%s</b>%s\n", number(s.Reading.Value), zone(s.Reading))
		writeReading(&b, s.Provider, s.Reading)
		b.WriteString("\n")
	}
	b.WriteString("Пороги:\n")
	fmt.Fprintf(&b, "😱 страх: %s\n", levels(alert.SideFear, s.Rules.Fear))
	fmt.Fprintf(&b, "🤑 жадность: %s\n\n", levels(alert.SideGreed, s.Rules.Greed))
	fmt.Fprintf(&b, "Проверка индекса: с интервалом %s, следующая %s UTC\n",
		humanDuration(s.Interval), s.NextCheck.UTC().Format(timeLayout))
	fmt.Fprintf(&b, "Пауза между повторными уведомлениями: %s", pause)
	return b.String()
}

// TestMessage is sent by the -test-message flag to check the setup.
func TestMessage(p fng.Provider, r fng.Reading) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✅ Бот подключён. <b>Fear &amp; Greed Index: %s</b>%s\n", number(r.Value), zone(r))
	writeReading(&b, p, r)
	return strings.TrimSuffix(b.String(), "\n")
}

const timeLayout = "02.01.2006 15:04"

// writeReading adds the BTC price, when known, and credits the provider next
// to the data, as alternative.me asks: "Данные alternative.me на 25.09.2026 00:00 UTC".
func writeReading(b *strings.Builder, p fng.Provider, r fng.Reading) {
	if r.Price > 0 {
		fmt.Fprintf(b, "BTC: %s\n", usd(r.Price))
	}
	parts := []string{"Данные"}
	if p.Name != "" {
		parts = append(parts, credit(p))
	}
	if !r.Time.IsZero() {
		parts = append(parts, "на "+r.Time.UTC().Format(timeLayout)+" UTC")
	}
	if len(parts) > 1 {
		b.WriteString(strings.Join(parts, " ") + "\n")
	}
}

// credit is the provider's name linked to its page.
func credit(p fng.Provider) string {
	name := html.EscapeString(p.Name)
	if p.URL == "" {
		return name
	}
	return `<a href="` + html.EscapeString(p.URL) + `">` + name + "</a>"
}

// zone is " (Greed)" for a reading with a known zone, otherwise empty.
func zone(r fng.Reading) string {
	if r.Zone == "" {
		return ""
	}
	return " (" + html.EscapeString(string(r.Zone)) + ")"
}

// truncate keeps at most n runes of s.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// sign shows how the index is compared with a level of the side.
func sign(side alert.Side) string {
	if side == alert.SideFear {
		return "≤"
	}
	return "≥"
}

// levels lists the levels of a side from the first reached to the deepest:
// "≤ 25, ≤ 10" for fear, "≥ 75, ≥ 90" for greed.
func levels(side alert.Side, ls []float64) string {
	if len(ls) == 0 {
		return "не отслеживается"
	}
	sorted := slices.Clone(ls)
	slices.Sort(sorted)
	if side == alert.SideFear {
		slices.Reverse(sorted)
	}
	parts := make([]string, len(sorted))
	for i, l := range sorted {
		parts[i] = sign(side) + " " + number(l)
	}
	return strings.Join(parts, ", ")
}

// humanDuration writes a duration in Russian: "4 часа", "1 день 12 часов".
func humanDuration(d time.Duration) string {
	units := []struct {
		size           time.Duration
		one, few, many string
	}{
		{24 * time.Hour, "день", "дня", "дней"},
		{time.Hour, "час", "часа", "часов"},
		{time.Minute, "минута", "минуты", "минут"},
		{time.Second, "секунда", "секунды", "секунд"},
	}
	var parts []string
	for _, u := range units {
		if n := int64(d / u.size); n > 0 {
			parts = append(parts, strconv.FormatInt(n, 10)+" "+plural(n, u.one, u.few, u.many))
			d -= time.Duration(n) * u.size
		}
	}
	if len(parts) == 0 {
		return "0 секунд"
	}
	return strings.Join(parts, " ")
}

// plural picks the Russian word form for a count: 1 час, 2 часа, 5 часов, 11 часов, 21 час.
func plural(n int64, one, few, many string) string {
	if n%100 >= 11 && n%100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
}

// number formats an index value or level without trailing zeros: 72, 72.5.
func number(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

// usd formats a price as whole dollars with thousands separators: $84,401.
func usd(v float64) string {
	digits := strconv.FormatInt(int64(math.Round(v)), 10)
	var b strings.Builder
	b.WriteByte('$')
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
