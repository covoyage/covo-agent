package recovery

import (
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

var (
	jitterCounter int64
	jitterMu      sync.Mutex
)

func JitteredBackoff(attempt int, baseDelay, maxDelay, jitterRatio float64) time.Duration {
	if baseDelay <= 0 {
		baseDelay = 5.0
	}
	if maxDelay <= 0 {
		maxDelay = 120.0
	}
	if jitterRatio <= 0 {
		jitterRatio = 0.5
	}

	jitterMu.Lock()
	jitterCounter++
	tick := jitterCounter
	jitterMu.Unlock()

	exponent := attempt - 1
	if exponent < 0 {
		exponent = 0
	}

	var delay float64
	if exponent >= 63 {
		delay = maxDelay
	} else {
		delay = math.Min(baseDelay*math.Pow(2, float64(exponent)), maxDelay)
	}

	seed := (uint64(time.Now().UnixNano()) ^ (uint64(tick) * 0x9E3779B9)) & 0xFFFFFFFF
	rng := rand.New(rand.NewPCG(seed, seed>>32))
	jitter := jitterRatio * delay * rng.Float64()

	return time.Duration((delay + jitter) * float64(time.Second))
}

func JitteredBackoffDefault(attempt int) time.Duration {
	return JitteredBackoff(attempt, 5.0, 120.0, 0.5)
}

type RetryOptions struct {
	MaxRetries  int
	BaseDelay   float64
	MaxDelay    float64
	JitterRatio float64
}

func DefaultRetryOptions() RetryOptions {
	return RetryOptions{
		MaxRetries:  3,
		BaseDelay:   5.0,
		MaxDelay:    120.0,
		JitterRatio: 0.5,
	}
}

func RetryWithBackoff(opts RetryOptions, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= opts.MaxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < opts.MaxRetries {
			delay := JitteredBackoff(attempt, opts.BaseDelay, opts.MaxDelay, opts.JitterRatio)
			time.Sleep(delay)
		}
	}
	return lastErr
}
