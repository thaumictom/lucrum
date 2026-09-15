package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thaumictom/lucrum/internal/clients"
	"github.com/thaumictom/lucrum/internal/config"
	"github.com/thaumictom/lucrum/internal/httpapi"
	"github.com/thaumictom/lucrum/internal/normalize"
	"github.com/thaumictom/lucrum/internal/storage"
	"github.com/thaumictom/lucrum/internal/workers"
)

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func archive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	data := `[{"uniqueName":"/a","name":"A","masterable":true,"components":[{"uniqueName":"/part","itemCount":2}]},{"uniqueName":"/part","name":"Part"}]`
	if err := writer.WriteHeader(&tar.Header{Name: "package/data/json/Items.json", Mode: 0644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	io.WriteString(writer, data)
	writer.Close()
	gz.Close()
	return buffer.Bytes()
}

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not reached")
}

func TestServiceWithOfflineUpstreams(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	releaseGate := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseGate) })
	tarball := archive(t)
	var statisticsCalls atomic.Int32
	var serverURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			select {
			case <-releaseGate:
			case <-r.Context().Done():
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"version": "1", "dist": map[string]string{"tarball": serverURL + "/archive"}})
		case "/archive":
			w.Write(tarball)
		case "/v2/items":
			io.WriteString(w, `{"data":[{"id":"id","gameRef":"/a","slug":"a","i18n":{"en":{"name":"A"}}}]}`)
		case "/v1/items/a/statistics":
			statisticsCalls.Add(1)
			io.WriteString(w, `{"payload":{"statistics_closed":{"90days":[{"datetime":"2026-09-14T00:00:00Z","volume":151}]},"statistics_live":{"48hours":[]}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	serverURL = upstream.URL
	store, err := storage.New(t.TempDir(), logger(), now)
	if err != nil {
		t.Fatal(err)
	}
	market, wfcd := clients.NewMarket(1000), clients.NewWFCD()
	market.BaseURL = upstream.URL
	wfcd.RegistryURL = upstream.URL + "/release"
	service := workers.New(store, market, wfcd, config.Config{CatalogInterval: time.Hour}, logger())
	service.Now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { service.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	// The blocked WFCD request cannot block the independent market worker.
	eventually(t, func() bool { return store.Health().TotalItems == 1 && store.Health().PendingItems == 0 })
	if store.Health().Ready {
		t.Fatal("ready before knowledge was published")
	}
	releaseOnce.Do(func() { close(releaseGate) })
	eventually(t, func() bool { return store.Health().Ready })
	cancel()
	<-done
	if statisticsCalls.Load() != 1 {
		t.Fatalf("unexpected fetches: %d", statisticsCalls.Load())
	}
	response := httptest.NewRecorder()
	httpapi.New(store).ServeHTTP(response, httptest.NewRequest("GET", "/api/tradeable-items", nil))
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"liquidity": 151`)) {
		t.Fatal(response.Body.String())
	}
}

// This opt-in test is the only test that contacts real upstream services.
func TestLiveUpstream(t *testing.T) {
	if os.Getenv("LUCRUM_LIVE_TEST") != "1" {
		t.Skip("set LUCRUM_LIVE_TEST=1 to contact public upstream services")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	wfcd := clients.NewWFCD()
	release, err := wfcd.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	builder := normalize.NewKnowledge(logger())
	if err := wfcd.Snapshot(ctx, release, builder.AddSource); err != nil {
		t.Fatal(err)
	}
	items, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("WFCD %s: %d canonical records", release.Version, len(items))
	market := clients.NewMarket(2.5)
	listings, err := market.Items(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("market listings: %d", len(listings))
	now := time.Now()
	dataDir := t.TempDir()
	store, err := storage.New(dataDir, logger(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishKnowledge(items, release.Version); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMarket(listings, now); err != nil {
		t.Fatal(err)
	}
	var sampleID string
	for _, item := range listings {
		if item.Slug == "arcane_energize" {
			sampleID = item.MarketID
		}
	}
	if sampleID == "" {
		t.Fatal("sample listing missing")
	}
	stats, err := market.Statistics(ctx, "arcane_energize")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStatistics(sampleID, stats, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishTradeable(time.Now()); err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(store)
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest("HEAD", "/api/items", nil))
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatal("HEAD failed")
	}
	request := httptest.NewRequest("GET", "/api/items", nil)
	request.Header.Set("If-None-Match", head.Header().Get("ETag"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 304 || response.Body.Len() != 0 {
		t.Fatal("conditional request failed")
	}
	restarted, err := storage.New(dataDir, logger(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if restarted.NeedsKnowledge(release.Version) {
		t.Fatal("restart did not recover the knowledge version")
	}
	restartedHead := httptest.NewRecorder()
	httpapi.New(restarted).ServeHTTP(restartedHead, httptest.NewRequest("HEAD", "/api/items", nil))
	if restartedHead.Header().Get("ETag") != head.Header().Get("ETag") {
		t.Fatal("restart changed the ETag")
	}
	t.Log(fmt.Sprintf("items.json bytes: %s; conditional GET: %d", head.Header().Get("Content-Length"), response.Code))
}
