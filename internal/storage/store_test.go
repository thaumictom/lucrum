package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/thaumictom/lucrum/internal/models"
	"github.com/thaumictom/lucrum/internal/normalize"
)

func testStore(t *testing.T, now time.Time) *Store {
	t.Helper()
	s, err := New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testMarket() []models.MarketItem {
	return []models.MarketItem{{MarketID: "id", GameRef: "/a", Slug: "a", Name: "A", Tags: []string{}}}
}

func TestAtomicSnapshotsValidatorsAndFailedWrites(t *testing.T) {
	store := testStore(t, time.Now())
	data := normalize.Knowledge{"/a": {"gameRef": "/a", "name": "A"}}
	if err := store.PublishKnowledge(data, "1"); err != nil {
		t.Fatal(err)
	}
	old, oldInfo, err := store.Open(ItemsFile)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := store.PublishKnowledge(data, "2"); err != nil {
		t.Fatal(err)
	}
	unchanged, same, err := store.Open(ItemsFile)
	if err != nil {
		t.Fatal(err)
	}
	unchanged.Close()
	if same.ETag != oldInfo.ETag || !same.ModTime.Equal(oldInfo.ModTime) {
		t.Fatal("unchanged content changed validator or time")
	}
	if err := store.PublishKnowledge(normalize.Knowledge{"/a": {"gameRef": "/a", "bad": make(chan int)}}, "bad"); err == nil {
		t.Fatal("accepted unencodable content")
	}
	file, info, err := store.Open(ItemsFile)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if info.ETag != oldInfo.ETag || store.NeedsKnowledge("2") {
		t.Fatal("failed write replaced old snapshot")
	}
	if err := store.PublishKnowledge(normalize.Knowledge{"/a": {"gameRef": "/a", "name": "B"}}, "3"); err != nil {
		t.Fatal(err)
	}
	oldBytes, _ := io.ReadAll(old)
	sum := sha256.Sum256(oldBytes)
	if oldInfo.ETag != `"`+hex.EncodeToString(sum[:])+`"` {
		t.Fatal("old open descriptor changed after publication")
	}
	restarted, err := New(store.dir, store.log, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reopened, recovered, err := restarted.Open(ItemsFile)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
	if recovered.ETag == oldInfo.ETag || restarted.NeedsKnowledge("3") {
		t.Fatal("restart lost content metadata/version")
	}
}

func TestStatisticsPersistenceRolloverAndReconciliation(t *testing.T) {
	now := time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC)
	store := testStore(t, now)
	if err := store.SetMarket(testMarket(), now); err != nil {
		t.Fatal(err)
	}
	if store.Health().PendingItems != 1 {
		t.Fatal("new listing not pending")
	}
	var row models.Row
	json.Unmarshal([]byte(`{"datetime":"2026-09-14T00:00:00Z","volume":151,"mod_rank":0}`), &row)
	if err := store.RecordStatistics("id", models.Statistics{Closed: []models.Row{row}, Live: []models.Row{}}, now); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(store.dir, store.log, now.Add(10*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	view := restarted.views["id"]
	if len(view.StatisticsToday) != 0 || len(view.StatisticsYesterday) != 1 || view.Liquidity != 151 || !view.LastFetchedAt.Equal(now) {
		t.Fatalf("restart/rollover lost cached statistics: %+v", view)
	}
	renamed := testMarket()
	renamed[0].Slug = "new-slug"
	if err := restarted.SetMarket(renamed, now.Add(10*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if restarted.views["id"].Slug != "new-slug" || restarted.views["id"].LastFetchedAt == nil {
		t.Fatal("slug change lost cached data")
	}
	other := []models.MarketItem{{MarketID: "other", Slug: "other", Name: "Other"}}
	if err := restarted.SetMarket(other, now); err != nil {
		t.Fatal(err)
	}
	if _, exists := restarted.cache["id"]; exists {
		t.Fatal("retired cache retained")
	}
	if err := restarted.RecordStatistics("id", models.Statistics{}, now); err != nil {
		t.Fatal(err)
	}
	if _, exists := restarted.cache["id"]; exists {
		t.Fatal("in-flight result resurrected retired listing")
	}
}

func TestConcurrentReadersSeeMatchingCompleteSnapshots(t *testing.T) {
	store := testStore(t, time.Now())
	if err := store.PublishKnowledge(normalize.Knowledge{"/a": {"gameRef": "/a", "generation": 0}}, "0"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				file, info, err := store.Open(ItemsFile)
				if err != nil {
					t.Error(err)
					return
				}
				data, err := io.ReadAll(file)
				file.Close()
				if err != nil || !json.Valid(data) {
					t.Error("incomplete snapshot")
					return
				}
				sum := sha256.Sum256(data)
				if info.ETag != `"`+hex.EncodeToString(sum[:])+`"` {
					t.Error("validator/body mismatch")
					return
				}
			}
		}()
	}
	for i := 1; i <= 10; i++ {
		if err := store.PublishKnowledge(normalize.Knowledge{"/a": {"gameRef": "/a", "generation": i}}, "v"); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}

func TestCorruptJSONRecoveryAndTemporaryCleanup(t *testing.T) {
	store := testStore(t, time.Now())
	os.WriteFile(store.dir+"/"+ItemsFile, []byte("{broken"), 0644)
	os.WriteFile(store.dir+"/.lucrum-abandoned.tmp", []byte("partial"), 0644)
	restarted, err := New(store.dir, store.log, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.NeedsKnowledge("1") {
		t.Fatal("corrupt JSON considered usable")
	}
	if _, err := os.Stat(store.dir + "/.lucrum-abandoned.tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file not cleaned")
	}
	os.WriteFile(store.dir+"/"+ItemsFile, []byte(`{"/a":{"gameRef":"/a","uniqueName":"/a"}}`), 0644)
	restarted, err = New(store.dir, store.log, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.NeedsKnowledge("1") {
		t.Fatal("accepted semantically invalid canonical cache")
	}
}

func TestConcurrentCatalogueUpdatesPreserveStatistics(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	store := testStore(t, now)
	if err := store.SetMarket(testMarket(), now); err != nil {
		t.Fatal(err)
	}
	var row models.Row
	json.Unmarshal([]byte(`{"datetime":"2026-09-14T00:00:00Z","volume":200}`), &row)
	stats := models.Statistics{Closed: []models.Row{row}, Live: []models.Row{}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			if err := store.SetMarket(testMarket(), now); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			if err := store.RecordStatistics("id", stats, now.Add(time.Duration(i)*time.Second)); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	cached := store.cache["id"]
	if !cached.LastFetchedAt.Equal(now.Add(9*time.Second)) || store.views["id"].Liquidity != 200 {
		t.Fatal("catalogue update lost newer statistics")
	}
	var invalid models.Row
	json.Unmarshal([]byte(`{"datetime":"invalid","volume":200}`), &invalid)
	if err := store.RecordStatistics("id", models.Statistics{Closed: []models.Row{invalid}}, now.Add(time.Hour)); err == nil {
		t.Fatal("accepted invalid statistics")
	}
	if !store.cache["id"].LastFetchedAt.Equal(cached.LastFetchedAt) {
		t.Fatal("failed fetch advanced cache timestamp")
	}
}

func TestMissingOutputsAreRebuiltEvenWhenUnchanged(t *testing.T) {
	now := time.Now()
	store := testStore(t, now)
	data := normalize.Knowledge{"/a": {"gameRef": "/a", "name": "A"}}
	if err := store.PublishKnowledge(data, "1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMarket(testMarket(), now); err != nil {
		t.Fatal(err)
	}
	for _, name := range publicFiles {
		if err := os.Remove(store.dir + "/" + name); err != nil {
			t.Fatal(err)
		}
	}
	if store.Health().Ready || !store.NeedsKnowledge("1") {
		t.Fatal("missing output was considered available")
	}
	if err := store.PublishKnowledge(data, "1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMarket(testMarket(), now); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.dir + "/" + TradeableFile); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishTradeable(now); err != nil {
		t.Fatal(err)
	}
	if !store.Health().Ready {
		t.Fatal("missing output was not restored")
	}
}
