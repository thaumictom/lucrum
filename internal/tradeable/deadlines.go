package tradeable

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lucrum/internal/snapshot"
)

// Deadlines are private: failed attempts must not change last_fetched_at in the
// public JSON. Workers reserve deadlines before network I/O, so this small map
// has its own lock. Only the coordinator owns the actual item statistics.
type deadlines struct {
	mu     sync.Mutex
	path   string
	values map[string]time.Time
}

func loadDeadlines(dir string) (*deadlines, error) {
	d := &deadlines{path: filepath.Join(dir, "tradeable-items.meta.json"), values: make(map[string]time.Time)}
	body, err := os.ReadFile(d.path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &d.values); err != nil {
		return nil, err
	}
	if d.values == nil {
		d.values = make(map[string]time.Time)
	}
	return d, nil
}

func (d *deadlines) get(slug string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.values[slug]
}

func (d *deadlines) set(slug string, next time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	old, existed := d.values[slug]
	d.values[slug] = next
	if err := d.save(); err != nil {
		if existed {
			d.values[slug] = old
		} else {
			delete(d.values, slug)
		}
		return err
	}
	return nil
}

func (d *deadlines) reconcile(entries []Item) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	next := make(map[string]time.Time, len(entries))
	for _, item := range entries {
		deadline := d.values[item.Slug]
		// Recover from a missing/older sidecar without needlessly refreshing a
		// successful snapshot. A newer failure deadline still takes precedence.
		if item.LastFetchedAt != nil {
			fromSnapshot := item.LastFetchedAt.Add(interval(item.Liquidity))
			if fromSnapshot.After(deadline) {
				deadline = fromSnapshot
			}
		}
		if !deadline.IsZero() {
			next[item.Slug] = deadline
		}
	}
	if maps.Equal(next, d.values) {
		return nil
	}
	old := d.values
	d.values = next
	if err := d.save(); err != nil {
		d.values = old
		return err
	}
	return nil
}

func (d *deadlines) save() error {
	body, err := json.Marshal(d.values)
	if err != nil {
		return err
	}
	return snapshot.Write(d.path, body)
}
