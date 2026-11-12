// Package fng contains the domain types of the Fear & Greed indexes.
package fng

import "time"

// Market is what an index measures the sentiment of.
type Market string

const (
	Crypto Market = "crypto" // the crypto market, mostly Bitcoin
	Stocks Market = "stocks" // the US stock market
)

// Reading is a single value of the index.
type Reading struct {
	Value float64   // 0 (extreme fear) .. 100 (extreme greed)
	Zone  Zone      // the provider's classification of Value, empty if unknown
	Time  time.Time // moment the value refers to, zero if unknown
	Price float64   // BTC price at Time, 0 if unknown
}

// Provider is who publishes the index. Providers differ in methodology, so
// their values differ, and alternative.me requires crediting it next to the data.
type Provider struct {
	Name string // shown in messages, e.g. "alternative.me"
	URL  string // page with the index
}

// Zone is the sentiment band of an index value. alternative.me, coinglass.com
// and CNN use these names, with different bands.
type Zone string

const (
	ExtremeFear  Zone = "Extreme Fear"
	Fear         Zone = "Fear"
	Neutral      Zone = "Neutral"
	Greed        Zone = "Greed"
	ExtremeGreed Zone = "Extreme Greed"
)

// Classify returns the zone of v using the bands of the CoinGlass page.
func Classify(v float64) Zone {
	switch {
	case v <= 20:
		return ExtremeFear
	case v <= 40:
		return Fear
	case v <= 60:
		return Neutral
	case v <= 80:
		return Greed
	default:
		return ExtremeGreed
	}
}
