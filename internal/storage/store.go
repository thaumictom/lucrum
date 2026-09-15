package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thaumictom/lucrum/internal/models"
	"github.com/thaumictom/lucrum/internal/normalize"
)

const (
	ItemsFile     = "items.json"
	MarketFile    = "warframe_market_items.json"
	TradeableFile = "tradeable_items.json"
)

var publicFiles = []string{ItemsFile, MarketFile, TradeableFile}

type RefreshStatus struct {
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastError     string     `json:"last_error,omitempty"`
}

type Health struct {
	Status       string                   `json:"status"`
	Ready        bool                     `json:"ready"`
	TotalItems   int                      `json:"total_items"`
	PendingItems int                      `json:"pending_statistics"`
	Refresh      map[string]RefreshStatus `json:"refresh"`
}

type Candidate struct {
	MarketID      string
	Slug          string
	LastFetchedAt *time.Time
	Liquidity     int64
}

type Store struct {
	mu      sync.RWMutex
	dir     string
	log     *slog.Logger
	files   map[string]FileInfo
	market  []models.MarketItem
	cache   map[string]models.Cache
	views   map[string]models.TradeableItem
	version string
	day     string
	dirty   bool
	refresh map[string]RefreshStatus
}

func New(dir string, log *slog.Logger, now time.Time) (*Store, error) {
	s := &Store{dir: dir, log: log, files: map[string]FileInfo{}, cache: map[string]models.Cache{},
		views: map[string]models.TradeableItem{}, refresh: map[string]RefreshStatus{}}
	for _, directory := range []string{dir, filepath.Join(dir, "private"), filepath.Join(dir, "private", "statistics")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), ".lucrum-") && strings.HasSuffix(entry.Name(), ".tmp") {
				if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, name := range publicFiles {
		info, err := inspect(filepath.Join(dir, name))
		if err == nil && name == ItemsFile {
			var knowledge normalize.Knowledge
			err = readJSON(filepath.Join(dir, name), &knowledge)
			if err == nil && len(knowledge) == 0 {
				err = fmt.Errorf("empty knowledge dataset")
			}
			if err == nil {
				err = normalize.ValidateKnowledge(knowledge)
			}
		}
		if err == nil {
			s.files[name] = info
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Warn("ignoring invalid published file", "file", name, "error", err)
		}
	}
	var source struct {
		Version string `json:"wfcd_version"`
	}
	if readJSON(filepath.Join(dir, "private", "source.json"), &source) == nil {
		s.version = source.Version
	}
	if _, exists := s.files[MarketFile]; exists {
		if err := readJSON(filepath.Join(dir, MarketFile), &s.market); err != nil || validateMarket(s.market) != nil {
			log.Warn("ignoring invalid market cache", "error", err)
			s.market = nil
			delete(s.files, MarketFile)
		}
	}
	for _, item := range s.market {
		var cached models.Cache
		if err := readJSON(s.cachePath(item.MarketID), &cached); err == nil {
			if cached.MarketID == item.MarketID && cached.Closed != nil && cached.Live != nil &&
				!cached.LastFetchedAt.IsZero() && !cached.LastFetchedAt.After(now) {
				if _, err := normalize.Retain(models.Statistics{Closed: cached.Closed, Live: cached.Live}, now); err == nil {
					s.cache[item.MarketID] = cached
				} else {
					log.Warn("ignoring invalid statistics cache", "market_id", item.MarketID, "error", err)
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Warn("ignoring unreadable statistics cache", "market_id", item.MarketID, "error", err)
		}
	}
	if len(s.market) > 0 {
		s.dirty = true
		if err := s.PublishTradeable(now); err != nil {
			return nil, err
		}
	} else {
		// Without listing identities, old public rows cannot be safely rebucketed.
		delete(s.files, TradeableFile)
	}
	return s, nil
}

func readJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func validateMarket(items []models.MarketItem) error {
	if len(items) == 0 {
		return fmt.Errorf("empty market catalogue")
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item.MarketID == "" || item.Slug == "" || item.Name == "" || seen[item.MarketID] {
			return fmt.Errorf("invalid market listing %q", item.MarketID)
		}
		seen[item.MarketID] = true
	}
	return nil
}

func (s *Store) cachePath(id string) string {
	hash := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, "private", "statistics", hex.EncodeToString(hash[:])+".json")
}

// commitLocked keeps the live file and HTTP metadata in the same generation.
func (s *Store) commitLocked(name string, temp prepared) error {
	if current, exists := s.files[name]; exists && current.ETag == temp.info.ETag {
		if _, err := os.Stat(filepath.Join(s.dir, name)); err == nil {
			return nil
		}
	}
	if err := os.Rename(temp.path, filepath.Join(s.dir, name)); err != nil {
		return err
	}
	// Rename is the publication point. Even if the directory sync fails, metadata
	// must describe the newly visible file rather than the previous generation.
	s.files[name] = temp.info
	return syncDir(s.dir)
}

func (s *Store) PublishKnowledge(items normalize.Knowledge, version string) error {
	if err := normalize.ValidateKnowledge(items); err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("cannot publish an empty knowledge dataset")
	}
	temp, err := prepareJSON(s.dir, items)
	if err != nil {
		return err
	}
	defer os.Remove(temp.path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.commitLocked(ItemsFile, temp); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(s.dir, "private", "source.json"), struct {
		Version string `json:"wfcd_version"`
	}{version}); err != nil {
		return err
	}
	s.version = version
	return nil
}

func (s *Store) NeedsKnowledge(version string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.files[ItemsFile]
	if !exists || s.version != version {
		return true
	}
	_, err := os.Stat(filepath.Join(s.dir, ItemsFile))
	return err != nil
}

func (s *Store) SetMarket(items []models.MarketItem, now time.Time) error {
	if err := validateMarket(items); err != nil {
		return err
	}
	items = append([]models.MarketItem(nil), items...)
	sort.Slice(items, func(i, j int) bool { return items[i].MarketID < items[j].MarketID })
	temp, err := prepareJSON(s.dir, items)
	if err != nil {
		return err
	}
	defer os.Remove(temp.path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.commitLocked(MarketFile, temp); err != nil {
		return err
	}
	s.market = items
	active := map[string]bool{}
	for _, item := range items {
		active[item.MarketID] = true
	}
	for id := range s.cache {
		if !active[id] {
			delete(s.cache, id)
			if err := os.Remove(s.cachePath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.log.Warn("could not remove retired cache", "market_id", id, "error", err)
			}
		}
	}
	s.day = ""
	s.dirty = true
	return s.publishTradeableLocked(now)
}

func (s *Store) RecordStatistics(id string, stats models.Statistics, now time.Time) error {
	retained, err := normalize.Retain(stats, now)
	if err != nil {
		return err
	}
	cached := models.Cache{MarketID: id, Closed: retained.Closed, Live: retained.Live, LastFetchedAt: now.UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.market {
		if item.MarketID != id {
			continue
		}
		view, err := normalize.Tradeable(item, &cached, now)
		if err != nil {
			return err
		}
		if err := atomicJSON(s.cachePath(id), cached); err != nil {
			return err
		}
		s.cache[id] = cached
		s.views[id] = view
		s.dirty = true
		return nil
	}
	// The catalogue may remove a listing while its HTTP request is in flight.
	return nil
}

func (s *Store) rebucketLocked(now time.Time) error {
	date := now.UTC().Format("2006-01-02")
	if date == s.day {
		return nil
	}
	views := make(map[string]models.TradeableItem, len(s.market))
	for _, item := range s.market {
		var cache *models.Cache
		if saved, ok := s.cache[item.MarketID]; ok {
			cache = &saved
		}
		view, err := normalize.Tradeable(item, cache, now)
		if err != nil {
			return err
		}
		views[item.MarketID] = view
	}
	s.views = views
	s.day = date
	s.dirty = true
	return nil
}

func (s *Store) publishTradeableLocked(now time.Time) error {
	if len(s.market) == 0 {
		return nil
	}
	if err := s.rebucketLocked(now); err != nil {
		return err
	}
	if !s.dirty {
		if _, err := os.Stat(filepath.Join(s.dir, TradeableFile)); err == nil {
			return nil
		}
	}
	items := make([]models.TradeableItem, 0, len(s.market))
	for _, item := range s.market {
		items = append(items, s.views[item.MarketID])
	}
	temp, err := prepareJSON(s.dir, items)
	if err != nil {
		return err
	}
	defer os.Remove(temp.path)
	if err := s.commitLocked(TradeableFile, temp); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func (s *Store) PublishTradeable(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.publishTradeableLocked(now)
}

func (s *Store) Candidates(now time.Time) ([]Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rebucketLocked(now); err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, len(s.market))
	for _, item := range s.market {
		view := s.views[item.MarketID]
		result = append(result, Candidate{item.MarketID, item.Slug, view.LastFetchedAt, view.Liquidity})
	}
	return result, nil
}

// Open captures the file descriptor and its validator under the same read lock.
// Linux keeps this descriptor valid even if a worker replaces the pathname.
func (s *Store) Open(name string) (*os.File, FileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, exists := s.files[name]
	if !exists {
		return nil, FileInfo{}, os.ErrNotExist
	}
	file, err := os.Open(filepath.Join(s.dir, name))
	return file, info, err
}

func (s *Store) Report(worker string, err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.refresh[worker]
	at := now.UTC()
	status.LastAttemptAt = &at
	status.LastError = ""
	if err == nil {
		status.LastSuccessAt = &at
	} else {
		status.LastError = err.Error()
	}
	s.refresh[worker] = status
}

func (s *Store) Health() Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	health := Health{Status: "ok", Ready: true, TotalItems: len(s.market), Refresh: map[string]RefreshStatus{}}
	for _, name := range publicFiles {
		if _, exists := s.files[name]; !exists {
			health.Ready = false
		} else if _, err := os.Stat(filepath.Join(s.dir, name)); err != nil {
			health.Ready = false
		}
	}
	for _, item := range s.market {
		if _, ok := s.cache[item.MarketID]; !ok {
			health.PendingItems++
		}
	}
	for key, value := range s.refresh {
		health.Refresh[key] = value
	}
	return health
}
