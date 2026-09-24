package bot

import (
	"fmt"
	"html"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"fear-and-greed-bot/internal/alert"
	"fear-and-greed-bot/internal/coinglass"
	"fear-and-greed-bot/internal/fng"
)

// Message is the Telegram notification (HTML) about a reached key level.
func Message(r fng.Reading, a alert.Alert) string {
	icon, what := "😱", "страха"
	if a.Side == alert.SideGreed {
		icon, what = "🤑", "жадности"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s <b>Fear &amp; Greed Index: %s</b> (%s)\n", icon, number(r.Value), html.EscapeString(string(fng.Classify(r.Value))))
	fmt.Fprintf(&b, "Достигнут ключевой уровень %s: %s %s\n", what, sign(a.Side), number(a.Level))
	writeDetails(&b, r)
	return b.String()
}

// StartMessage announces that the bot is running, with its levels and schedule.
func StartMessage(rules alert.Rules, interval, cooldown time.Duration) string {
	pause := "нет"
	if cooldown > 0 {
		pause = humanDuration(cooldown)
	}
	var b strings.Builder
	b.WriteString("🚀 <b>Fear &amp; Greed бот запущен</b>\n\n")
	b.WriteString("Пороги:\n")
	fmt.Fprintf(&b, "😱 страх: %s\n", levels(alert.SideFear, rules.Fear))
	fmt.Fprintf(&b, "🤑 жадность: %s\n\n", levels(alert.SideGreed, rules.Greed))
	fmt.Fprintf(&b, "Проверка индекса: сразу после запуска, затем с интервалом %s\n", humanDuration(interval))
	fmt.Fprintf(&b, "Пауза между повторными уведомлениями: %s\n", pause)
	b.WriteString(coinglass.PageURL)
	return b.String()
}

// TestMessage is sent by the -test-message flag to check the setup.
func TestMessage(r fng.Reading) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✅ Бот подключён. <b>Fear &amp; Greed Index: %s</b> (%s)\n", number(r.Value), html.EscapeString(string(fng.Classify(r.Value))))
	writeDetails(&b, r)
	return b.String()
}

func writeDetails(b *strings.Builder, r fng.Reading) {
	if r.Price > 0 {
		fmt.Fprintf(b, "BTC: %s\n", usd(r.Price))
	}
	if !r.Time.IsZero() {
		fmt.Fprintf(b, "Данные на %s UTC\n", r.Time.UTC().Format("02.01.2006 15:04"))
	}
	b.WriteString(coinglass.PageURL)
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
