package download

import (
	"context"
	"errors"
	"fmt"
	"time"

	upstreamnntp "github.com/Tensai75/nntp"

	"nzbgrab/internal/nntp"
	"nzbgrab/internal/nzb"
	"nzbgrab/internal/yenc"
)

const (
	// maxSegmentAttempts is how many times a single segment fetch is attempted
	// before giving up. Unstable connections (resets, timeouts) are retried so
	// downloads can auto-recover without user intervention.
	maxSegmentAttempts = 50

	// retryBaseDelay and retryMaxDelay bound the exponential backoff between
	// retry attempts.
	retryBaseDelay = 500 * time.Millisecond
	retryMaxDelay  = 15 * time.Second
)

// fetchAndDecodeSegment downloads and yEnc-decodes a single segment, retrying
// on transient network errors up to maxSegmentAttempts times. It returns the
// decoded data and the filename from the yEnc header (if present).
func fetchAndDecodeSegment(ctx context.Context, pool *nntp.Pool, segment *nzb.Segment) ([]byte, string, error) {
	var lastErr error
	for attempt := 1; attempt <= maxSegmentAttempts; attempt++ {
		if attempt > 1 {
			if err := sleepBackoff(ctx, attempt); err != nil {
				return nil, "", err
			}
		}

		data, err := pool.FetchArticle(ctx, segment.MessageID)
		if err == nil {
			var result *yenc.Result
			result, err = yenc.DecodeBytes(data)
			if err == nil {
				return result.Data, result.Header.Name, nil
			}
			err = fmt.Errorf("yenc decode: %w", err)
		}

		// Don't keep retrying if the caller cancelled or the article is
		// permanently unavailable (e.g. removed from the server).
		if ctx.Err() != nil {
			return nil, "", err
		}
		if !isRetryable(err) {
			return nil, "", err
		}
		lastErr = err
	}
	return nil, "", fmt.Errorf("giving up after %d attempts: %w", maxSegmentAttempts, lastErr)
}

// isRetryable reports whether an error from fetching/decoding a segment is
// worth retrying. Transient problems (connection resets, timeouts, truncated
// bodies) are retryable; a permanent server response such as 430 "no such
// article" is not, since retrying it would only waste time.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var protoErr upstreamnntp.Error
	if errors.As(err, &protoErr) {
		switch protoErr.Code {
		// Article genuinely not available on the server — won't recover.
		case 430, // no such article
			423, // no such article number in this group
			420: // no current article selected
			return false
		}
	}
	return true
}

// backoffDelay returns the delay to wait before the given attempt, growing
// exponentially from retryBaseDelay and clamped to retryMaxDelay. The shift is
// guarded so large attempt numbers can't overflow into a bogus delay.
func backoffDelay(attempt int) time.Duration {
	shift := attempt - 2 // attempt 2 -> base, 3 -> 2x, ...
	if shift < 0 {
		shift = 0
	}
	if shift > 31 {
		return retryMaxDelay
	}
	delay := retryBaseDelay << uint(shift)
	if delay <= 0 || delay > retryMaxDelay {
		return retryMaxDelay
	}
	return delay
}

// sleepBackoff waits for an exponentially increasing delay before the given
// attempt, returning early if the context is cancelled.
func sleepBackoff(ctx context.Context, attempt int) error {
	timer := time.NewTimer(backoffDelay(attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
