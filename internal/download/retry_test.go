package download

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	upstreamnntp "github.com/Tensai75/nntp"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"connection reset", &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}, true},
		{"wrapped network error", fmt.Errorf("reading body: %w", &net.OpError{Op: "read", Err: errors.New("boom")}), true},
		{"no such article 430", upstreamnntp.Error{Code: 430, Msg: "no such article"}, false},
		{"wrapped 430", fmt.Errorf("fetching body: %w", upstreamnntp.Error{Code: 430}), false},
		{"no such article number 423", upstreamnntp.Error{Code: 423}, false},
		{"temporary server error 400", upstreamnntp.Error{Code: 400}, true},
		{"generic error", errors.New("something else"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err); got != tt.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestSleepBackoffRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	start := time.Now()
	err := sleepBackoff(ctx, 10) // would otherwise sleep up to retryMaxDelay
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("sleepBackoff did not return promptly on cancellation: %v", elapsed)
	}
}

func TestSleepBackoffIsBounded(t *testing.T) {
	// A high attempt number must not overflow into a negative/huge delay; it
	// should be clamped to retryMaxDelay.
	if d := backoffDelay(2); d != retryBaseDelay {
		t.Errorf("attempt 2 delay = %v, want %v", d, retryBaseDelay)
	}
	if d := backoffDelay(3); d != 2*retryBaseDelay {
		t.Errorf("attempt 3 delay = %v, want %v", d, 2*retryBaseDelay)
	}
	if d := backoffDelay(50); d != retryMaxDelay {
		t.Errorf("attempt 50 delay = %v, want clamp to %v", d, retryMaxDelay)
	}
}
