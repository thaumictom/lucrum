// Package tradeable refreshes cached item statistics in hourly passes.
package tradeable

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/url"
	"os"
	"time"

	"lucrum/internal/items"
	"lucrum/internal/snapshot"
	"lucrum/internal/upstream"
)

const maxInFlight = 8

type Service struct {
	*snapshot.File
	catalogue  *items.Store
	client     *upstream.Client
	deadlines  *deadlines
	entries    []Item
	dirty      bool
	checkpoint int
}

type result struct {
	index   int
	item    Item
	started time.Time
	err     error
}

func New(dir string, catalogue *items.Store, client *upstream.Client, rate float64) (*Service, error) {
	file, err := snapshot.New(dir, "tradeable-items.json", validateSaved)
	if err != nil {
		return nil, err
	}
	deadlines, err := loadDeadlines(dir)
	if err != nil {
		return nil, err
	}
	s := &Service{File: file, catalogue: catalogue, client: client, deadlines: deadlines, entries: []Item{}, checkpoint: max(1, int(math.Ceil(rate*60)))}
	if file.Available() {
		body, err := file.Read()
		if err != nil {
			return nil, err
		}
		var saved document
		if err := json.Unmarshal(body, &saved); err != nil {
			return nil, err
		}
		s.entries = saved.Items
	}
	return s, nil
}

// nextPass always returns a future :15 UTC. Recomputing after each tick avoids
// replaying missed hours after a long pause or a slow pass.
func nextPass(now time.Time) time.Time {
	next := now.UTC().Truncate(time.Hour).Add(15 * time.Minute)
	if !next.After(now) {
		next = next.Add(time.Hour)
	}
	return next
}

func (s *Service) Run(ctx context.Context) {
	if !s.Available() {
		if s.reconcile() == nil {
			s.publish()
		}
	}
	next := nextPass(time.Now())
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	slog.Info("statistics scheduler ready", "next_pass", next, "publish_every_requests", s.checkpoint)

	// Only this loop changes entries and pass counters. Workers return values
	// through a channel, much like promises resolved back to a TS coordinator.
	results := make(chan result, maxInFlight)
	var starting <-chan struct{}
	var queue []int
	active, stopping := false, false
	inFlight, completed, sinceCheckpoint, failed := 0, 0, 0, 0
	done := ctx.Done()
	for {
		if !stopping && ctx.Err() == nil {
			if active && len(queue) > 0 && inFlight < maxInFlight && starting == nil {
				index := queue[0]
				queue = queue[1:]
				base := s.entries[index]
				inFlight++
				started := make(chan struct{})
				starting = started
				go func() { results <- s.fetch(ctx, index, base, started) }()
			}
		}
		if active && len(queue) == 0 && inFlight == 0 {
			s.publish()
			slog.Info("statistics pass completed", "requests", completed, "failed", failed)
			active = false
		}
		if stopping && inFlight == 0 {
			s.publish()
			return
		}

		select {
		case <-starting:
			// Wait only for the request to start, not finish, before dispatching
			// the next slug. This preserves queue order despite concurrent I/O.
			starting = nil
		case <-done:
			stopping = true
			done = nil // A nil channel disables this select case while we drain workers.
			queue = nil
		case <-s.catalogue.Changes:
			// Initialise an empty deployment promptly. Once published, catalogue
			// changes are deliberately picked up only at the next pass boundary.
			if !s.Available() && !active && !stopping {
				if s.reconcile() == nil {
					s.publish()
				}
			}
		case <-timer.C:
			next = nextPass(time.Now())
			timer.Reset(time.Until(next))
			if active || stopping || ctx.Err() != nil {
				slog.Debug("statistics pass skipped", "reason", "previous pass active or shutting down", "next_pass", next)
				continue
			}
			if err := s.reconcile(); err != nil {
				continue
			}
			queue = nil
			now := time.Now()
			for index, item := range s.entries {
				deadline := s.deadlines.get(item.Slug)
				if deadline.After(now) {
					slog.Debug("statistics fetch skipped", "slug", item.Slug, "next_fetch", deadline)
					continue
				}
				queue = append(queue, index) // Catalogue order; no sorting.
			}
			active = true
			completed, sinceCheckpoint, failed = 0, 0, 0
			if len(queue) == 0 {
				slog.Debug("statistics requests skipped", "reason", "no due items")
			}
			slog.Info("statistics pass started", "due", len(queue), "items", len(s.entries))
		case result := <-results:
			inFlight--
			if !result.started.IsZero() {
				completed++
				sinceCheckpoint++
			}
			if result.err != nil {
				failed++
				slog.Warn("statistics fetch failed; keeping previous data", "slug", s.entries[result.index].Slug, "error", result.err, "next_fetch", s.deadlines.get(s.entries[result.index].Slug))
			} else {
				s.entries[result.index] = result.item
				s.dirty = true
				next := result.started.Add(interval(result.item.Liquidity))
				if err := s.deadlines.set(result.item.Slug, next); err != nil {
					slog.Error("could not save successful refresh deadline", "slug", result.item.Slug, "error", err)
				}
				slog.Debug("statistics fetch completed", "slug", result.item.Slug, "liquidity", result.item.Liquidity, "next_fetch", next)
			}
			if sinceCheckpoint >= s.checkpoint {
				s.publish()
				sinceCheckpoint = 0
			}
		}
	}
}

func (s *Service) fetch(ctx context.Context, index int, base Item, announced chan<- struct{}) result {
	startedRequest := false
	defer func() {
		if !startedRequest {
			close(announced) // Also unblock dispatch if cancellation prevents a start.
		}
	}()
	endpoint := "https://api.warframe.market/v1/items/" + url.PathEscape(base.Slug) + "/statistics"
	body, started, err := s.client.Fetch(ctx, endpoint, func(started time.Time) error {
		// This callback runs after rate-limit waiting, immediately before I/O.
		if err := s.deadlines.set(base.Slug, started.Add(interval(base.Liquidity))); err != nil {
			return err
		}
		startedRequest = true
		close(announced)
		return nil
	})
	var updated Item
	if err == nil {
		updated, err = transform(body, base, started)
	}
	return result{index: index, item: updated, started: started, err: err}
}

func (s *Service) reconcile() error {
	catalogue, err := s.catalogue.Catalogue()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("statistics pass skipped", "reason", "catalogue not available")
		} else {
			slog.Error("read catalogue for statistics", "error", err)
		}
		return err
	}
	previous := make(map[string]Item, len(s.entries))
	for _, item := range s.entries {
		previous[item.Slug] = item
	}
	entries := make([]Item, 0, len(catalogue))
	changed := !s.Available() || len(catalogue) != len(s.entries)
	for index, source := range catalogue {
		item, exists := previous[source.Slug]
		if !exists {
			item = Item{Slug: source.Slug, Today: []row{}, Yesterday: []row{}, Live: []row{}}
		}
		if !exists || item.Name != source.Name || item.GameRef != source.GameRef || index >= len(s.entries) || s.entries[index].Slug != source.Slug {
			changed = true
		}
		item.Name, item.GameRef = source.Name, source.GameRef
		entries = append(entries, item)
	}
	if err := s.deadlines.reconcile(entries); err != nil {
		slog.Error("reconcile refresh deadlines", "error", err)
		return err
	}
	s.entries = entries
	s.dirty = s.dirty || changed
	return nil
}

func (s *Service) publish() {
	if !s.dirty {
		return
	}
	body, err := json.Marshal(document{Items: s.entries})
	if err == nil {
		err = s.Publish(body)
	}
	if err != nil {
		slog.Error("could not publish tradeable items; keeping previous file", "error", err)
		return // Keep dirty state for the next checkpoint or final publication.
	}
	s.dirty = false
	slog.Info("tradeable items published", "items", len(s.entries), "bytes", len(body))
}
