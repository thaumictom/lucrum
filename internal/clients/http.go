// Package clients handles upstream HTTP, shared pacing, and snapshot downloads.
package clients

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const userAgent = "Lucrum/0.1 (+https://github.com/thaumictom/lucrum)"

// Limiter spaces request starts. No tokens accumulate while the service is idle.
// A shared cooldown also delays requests that are already waiting.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	blocked  time.Time
}

func NewLimiter(rate float64) *Limiter {
	return &Limiter{interval: time.Duration(float64(time.Second) / rate)}
}

func (l *Limiter) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		now := time.Now()
		until := l.next
		if l.blocked.After(until) {
			until = l.blocked
		}
		if !now.Before(until) {
			l.next = now.Add(l.interval)
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
		if err := sleep(ctx, time.Until(until)); err != nil {
			return err
		}
	}
}

func (l *Limiter) Pause(delay time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := time.Now().Add(delay); until.After(l.blocked) {
		l.blocked = until
	}
}

type HTTP struct {
	Client  *http.Client
	Limiter *Limiter
	Market  bool
	// These functions let tests exercise retries without waiting real minutes.
	sleep func(context.Context, time.Duration) error
	now   func() time.Time
}

func newHTTP(timeout time.Duration) *HTTP {
	return &HTTP{Client: &http.Client{Timeout: timeout}, sleep: sleep, now: time.Now}
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Get retries temporary failures. The caller owns and must close a successful body.
func (h *HTTP) Get(ctx context.Context, url string) (*http.Response, error) {
	const attempts = 4
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if h.Limiter != nil {
			if err := h.Limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		if h.Market {
			req.Header.Set("Platform", "pc")
			req.Header.Set("Crossplay", "true")
			req.Header.Set("Language", "en")
		}
		response, err := h.Client.Do(req)
		delay := time.Second*time.Duration(1<<attempt) + time.Duration(rand.Int64N(int64(250*time.Millisecond)))
		if err == nil {
			if response.StatusCode == http.StatusOK {
				return response, nil
			}
			last = fmt.Errorf("GET %s: HTTP %d", url, response.StatusCode)
			if retry := retryAfter(response.Header.Get("Retry-After"), h.now()); retry > delay {
				delay = retry
			}
			io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			response.Body.Close()
			retryable := response.StatusCode == 429 || response.StatusCode >= 500
			if !retryable {
				return nil, last
			}
			if h.Limiter != nil && (response.StatusCode == 429 || response.StatusCode == 509) {
				h.Limiter.Pause(delay)
			}
		} else {
			last = fmt.Errorf("GET %s: %w", url, err)
		}
		if attempt+1 < attempts {
			if err := h.sleep(ctx, delay); err != nil {
				return nil, err
			}
		}
	}
	return nil, last
}

func retryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		// Avoid overflowing a duration on a malformed upstream header.
		if seconds <= int64(time.Duration(1<<63-1)/time.Second) {
			return time.Duration(seconds) * time.Second
		}
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}
