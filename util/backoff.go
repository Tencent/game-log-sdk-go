package util

import (
	"math"
	"math/rand"
	"time"
)

// ExponentialBackoff implements an exponential backoff strategy
type ExponentialBackoff struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	Multiplier      float64
	Randomization   float64
}

// Next calculates the next interval with exponential backoff
func (b *ExponentialBackoff) Next(retryCount int) time.Duration {
	if retryCount <= 0 {
		return b.InitialInterval
	}

	interval := float64(b.InitialInterval) * math.Pow(b.Multiplier, float64(retryCount-1))
	if b.Randomization > 0 {
		interval = interval * (1 + b.Randomization*(rand.Float64()*2-1))
	}

	if interval > float64(b.MaxInterval) {
		interval = float64(b.MaxInterval)
	}

	return time.Duration(interval)
}
