package bot

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"

	"fear-and-greed-bot/internal/alert"
	"fear-and-greed-bot/internal/coinglass"
	"fear-and-greed-bot/internal/fng"
)

// Message is the Telegram notification (HTML) about a reached key level.
func Message(r fng.Reading, a alert.Alert) string {
	icon, what, cmp := "😱", "страха", "≤"
	if a.Side == alert.SideGreed {
		icon, what, cmp = "🤑", "жадности", "≥"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s <b>Fear &amp; Greed Index: %s</b> (%s)\n", icon, number(r.Value), html.EscapeString(string(fng.Classify(r.Value))))
	fmt.Fprintf(&b, "Достигнут ключевой уровень %s: %s %s\n", what, cmp, number(a.Level))
	writeDetails(&b, r)
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
