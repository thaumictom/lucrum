// Package upstream shares request-start spacing and 429 pauses across WFM APIs.
package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"lucrum/internal/snapshot"
)

type Client struct {
	http *http.Client
}

type transport struct {
	base        http.RoundTripper
	mu          sync.Mutex
	spacing     time.Duration
	nextStart   time.Time
	pausedUntil time.Time
	pausePath   string
}

// This state belongs to one Fetch call; redirects share it. The callback is
// called only for its first request, immediately before starting network I/O.
type attempt struct {
	started time.Time
	before  func(time.Time) error
}

type attemptKey struct{}

func New(dir string, requestsPerSecond float64) (*Client, error) {
	if requestsPerSecond <= 0 || requestsPerSecond > 3 || math.IsNaN(requestsPerSecond) {
		return nil, errors.New("WFM_REQUESTS_PER_SECOND must be greater than 0 and at most 3")
	}
	spacing := math.Ceil(float64(time.Second) / requestsPerSecond)
	if spacing >= float64(math.MaxInt64) {
		return nil, errors.New("WFM_REQUESTS_PER_SECOND is too small for a Go duration")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	t := &transport{
		base:      http.DefaultTransport.(*http.Transport).Clone(),
		spacing:   time.Duration(spacing),
		pausePath: filepath.Join(dir, "wfm-pause.json"),
	}
	body, err := os.ReadFile(t.pausePath)
	if err == nil {
		if err := json.Unmarshal(body, &t.pausedUntil); err != nil {
			return nil, fmt.Errorf("read saved WFM pause: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &Client{http: &http.Client{Transport: t}}, nil
}

// Fetch bounds body size and returns the first HTTP request's actual start
// time. Time spent waiting for the limiter does not consume the HTTP timeout.
func (c *Client) Fetch(ctx context.Context, url string, before func(time.Time) error) ([]byte, time.Time, error) {
	a := &attempt{before: before}
	ctx = context.WithValue(ctx, attemptKey{}, a)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, a.started, err
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, a.started, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, a.started, fmt.Errorf("upstream returned %s", response.Status)
	}
	const maxBytes = 32 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err == nil && len(body) > maxBytes {
		err = errors.New("upstream response exceeds 32 MiB")
	}
	slog.Debug("fetch body completed", "url", url, "bytes", len(body), "error", err)
	return body, a.started, err
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	a := req.Context().Value(attemptKey{}).(*attempt)
	if err := t.wait(req.Context(), a); err != nil {
		return nil, err
	}
	// Each redirect goes through this transport and therefore the same limiter.
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	started := time.Now()
	slog.Debug("fetch started", "url", req.URL.String(), "started_at", started.UTC())
	response, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		cancel()
		slog.Debug("fetch completed", "url", req.URL.String(), "duration", time.Since(started), "error", err)
		return nil, err
	}
	if response.StatusCode == http.StatusTooManyRequests {
		t.pause(response.Header.Get("Retry-After"))
	}
	slog.Debug("fetch response", "url", req.URL.String(), "status", response.StatusCode, "duration", time.Since(started))
	// Keep the timeout active while the caller reads the body, not just headers.
	response.Body = &cancelBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

func (t *transport) wait(ctx context.Context, a *attempt) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		t.mu.Lock()
		deadline := t.nextStart
		if t.pausedUntil.After(deadline) {
			deadline = t.pausedUntil
		}
		if delay := time.Until(deadline); delay > 0 {
			t.mu.Unlock()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				continue // Recheck in case a 429 extended the pause.
			}
		}
		started := time.Now().UTC()
		if a.started.IsZero() && a.before != nil {
			if err := a.before(started); err != nil {
				t.mu.Unlock()
				return err
			}
		}
		if a.started.IsZero() {
			a.started = started
		}
		// Reserve from now, not an old timer deadline: idle time cannot accumulate
		// tokens. Release the lock before I/O so slow requests can overlap.
		t.nextStart = time.Now().Add(t.spacing)
		t.mu.Unlock()
		return nil
	}
}

func (t *transport) pause(header string) {
	now := time.Now().UTC()
	until := now.Add(30 * time.Second)
	header = strings.TrimSpace(header)
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil && seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second) {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(header); err == nil && date.After(now) {
		until = date
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !until.After(t.pausedUntil) {
		return
	}
	t.pausedUntil = until
	body, err := json.Marshal(until)
	if err == nil {
		err = snapshot.Write(t.pausePath, body)
	}
	if err != nil {
		slog.Error("could not persist WFM pause", "error", err)
	}
	slog.Warn("WFM rate limit: pausing request starts", "until", until)
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}
