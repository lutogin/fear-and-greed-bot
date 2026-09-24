// Package fng contains the domain types of the Crypto Fear & Greed Index.
package fng

import "time"

// Reading is a single value of the index.
type Reading struct {
	Value float64   // 0 (extreme fear) .. 100 (extreme greed)
	Time  time.Time // moment the value refers to, zero if unknown
	Price float64   // BTC price at Time, 0 if unknown
}

// Zone is the sentiment band of an index value, named as on coinglass.com.
type Zone string

const (
	ExtremeFear  Zone = "Extreme Fear"
	Fear         Zone = "Fear"
	Neutral      Zone = "Neutral"
	Greed        Zone = "Greed"
	ExtremeGreed Zone = "Extreme Greed"
)

// Classify returns the zone of v using the same bands as the CoinGlass page.
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
