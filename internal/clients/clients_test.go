package clients

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeadersAndRetries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key, want := range map[string]string{"Platform": "pc", "Crossplay": "true", "Language": "en", "User-Agent": userAgent} {
			if got := r.Header.Get(key); got != want {
				t.Errorf("%s=%q", key, got)
			}
		}
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			return
		}
		io.WriteString(w, `{"data":[{"id":"a","slug":"a","gameRef":"","i18n":{"en":{"name":"A"}}},{"id":"b","slug":"b","gameRef":"","i18n":{"en":{"name":"B"}}}]}`)
	}))
	defer server.Close()
	market := NewMarket(1000)
	market.BaseURL = server.URL
	var slept time.Duration
	market.HTTP.sleep = func(ctx context.Context, d time.Duration) error {
		slept = d
		market.HTTP.Limiter.mu.Lock()
		if time.Until(market.HTTP.Limiter.blocked) < time.Second {
			t.Error("429 did not set a shared cooldown")
		}
		// Simulate time passing without a two-second test.
		market.HTTP.Limiter.blocked = time.Time{}
		market.HTTP.Limiter.mu.Unlock()
		return nil
	}
	items, err := market.Items(context.Background())
	if err != nil || len(items) != 2 || requests.Load() != 2 || slept < 2*time.Second {
		t.Fatalf("%v %v %v", items, err, slept)
	}
}

func TestLimiterSharedPacingAndCancellation(t *testing.T) {
	limiter := NewLimiter(100)
	times := make(chan time.Time, 5)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := limiter.Wait(context.Background()); err != nil {
				t.Error(err)
			}
			times <- time.Now()
		}()
	}
	wg.Wait()
	close(times)
	var first, last time.Time
	for at := range times {
		if first.IsZero() || at.Before(first) {
			first = at
		}
		if at.After(last) {
			last = at
		}
	}
	if last.Sub(first) < 35*time.Millisecond {
		t.Fatal("requests burst instead of being spaced")
	}
	limiter.Pause(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.Wait(ctx); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestRetryAfterAndPermanentErrors(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"15", 15 * time.Second}, {now.Add(time.Minute).Format(http.TimeFormat), time.Minute}, {"-1", 0}, {"invalid", 0},
	} {
		if got := retryAfter(test.value, now); got != test.want {
			t.Errorf("%q: %v", test.value, got)
		}
	}
	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; w.WriteHeader(404) }))
	defer server.Close()
	h := newHTTP(time.Second)
	if _, err := h.Get(context.Background(), server.URL); err == nil || count != 1 {
		t.Fatal("permanent error retried")
	}
}

func TestStatisticsEnvelope(t *testing.T) {
	response := `{"payload":{"statistics_closed":{"90days":[]},"statistics_live":{"48hours":[]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, response) }))
	defer server.Close()
	client := NewMarket(10000)
	client.BaseURL = server.URL
	if _, err := client.Statistics(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	response = `{"payload":{"statistics_closed":{},"statistics_live":{"48hours":[]}}}`
	if _, err := client.Statistics(context.Background(), "a"); err == nil {
		t.Fatal("accepted missing interval")
	}
}

func TestSnapshotStreamingIntegrityAndMalformedArchive(t *testing.T) {
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for _, file := range []struct{ name, body string }{
		{"package/data/json/Items.json", `[{"uniqueName":"/a","tradable":true}]`},
		{"package/data/json/i18n.json", "ignored"},
		{"../../escape.json", "ignored"},
		{"package/script.js", "ignored"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Size: int64(len(file.body)), Mode: 0644}); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, file.body)
	}
	tw.Close()
	gz.Close()
	body := data.Bytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer server.Close()
	client := NewWFCD()
	release := Release{Version: "v"}
	release.Dist.Tarball = server.URL
	sum := sha512.Sum512(body)
	release.Dist.Integrity = "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
	var files []string
	err := client.Snapshot(context.Background(), release, func(name string, r io.Reader) error {
		files = append(files, name)
		got, err := io.ReadAll(r)
		if !strings.Contains(string(got), "/a") {
			return fmt.Errorf("unexpected file contents")
		}
		return err
	})
	if err != nil || !reflect.DeepEqual(files, []string{"Items.json"}) {
		t.Fatal(files, err)
	}
	release.Dist.Integrity = "sha512-wrong"
	if err := client.Snapshot(context.Background(), release, func(string, io.Reader) error { return nil }); err == nil {
		t.Fatal("accepted wrong digest")
	}
	body = body[:len(body)/2]
	if err := client.Snapshot(context.Background(), release, func(string, io.Reader) error { return nil }); err == nil {
		t.Fatal("accepted truncated archive")
	}
}
