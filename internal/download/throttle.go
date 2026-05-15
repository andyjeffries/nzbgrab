// Package download handles downloading NZB files.
package download

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter provides shared bandwidth limiting across all downloads.
type RateLimiter struct {
	limiter *rate.Limiter
	mu      sync.Mutex
}

// NewRateLimiter creates a new rate limiter.
// bytesPerSec is the maximum bytes per second (0 = unlimited).
func NewRateLimiter(bytesPerSec int64) *RateLimiter {
	if bytesPerSec <= 0 {
		return &RateLimiter{}
	}

	// Burst size allows some burstiness for efficiency
	// Use 1 second worth of data, capped at 1MB
	burst := int(bytesPerSec)
	if burst > 1024*1024 {
		burst = 1024 * 1024
	}
	if burst < 64*1024 {
		burst = 64 * 1024
	}

	return &RateLimiter{
		limiter: rate.NewLimiter(rate.Limit(bytesPerSec), burst),
	}
}

// Wait blocks until n bytes can be consumed, respecting the rate limit.
func (r *RateLimiter) Wait(ctx context.Context, n int) error {
	if r.limiter == nil {
		return nil
	}
	return r.limiter.WaitN(ctx, n)
}

// Limit returns the current rate limit in bytes per second.
func (r *RateLimiter) Limit() int64 {
	if r.limiter == nil {
		return 0
	}
	return int64(r.limiter.Limit())
}

// ParseBandwidthLimit parses a human-readable bandwidth limit.
// Examples: "10M", "500K", "1G" (case insensitive)
// Returns bytes per second, or 0 if empty/invalid.
func ParseBandwidthLimit(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	// Find where the number ends and suffix begins
	var numEnd int
	for i, c := range s {
		if (c >= '0' && c <= '9') || c == '.' {
			numEnd = i + 1
		} else {
			break
		}
	}

	if numEnd == 0 {
		return 0
	}

	numStr := s[:numEnd]
	suffix := strings.ToUpper(strings.TrimSpace(s[numEnd:]))

	var value float64
	if _, err := fmt.Sscanf(numStr, "%f", &value); err != nil {
		return 0
	}

	// Apply multiplier based on suffix
	switch suffix {
	case "K", "KB":
		value *= 1024
	case "M", "MB":
		value *= 1024 * 1024
	case "G", "GB":
		value *= 1024 * 1024 * 1024
	}

	return int64(value)
}
