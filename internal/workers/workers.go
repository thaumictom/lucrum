// Package workers runs the independent catalogue and market refresh loops.
package workers

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/thaumictom/lucrum/internal/clients"
	"github.com/thaumictom/lucrum/internal/config"
	"github.com/thaumictom/lucrum/internal/models"
	"github.com/thaumictom/lucrum/internal/normalize"
	"github.com/thaumictom/lucrum/internal/storage"
)

type Market interface {
	Items(context.Context) ([]models.MarketItem, error)
	Statistics(context.Context, string) (models.Statistics, error)
}

type WFCD interface {
	Latest(context.Context) (clients.Release, error)
	Snapshot(context.Context, clients.Release, func(string, io.Reader) error) error
}

type Service struct {
	Store    *storage.Store
	Market   Market
	WFCD     WFCD
	Interval time.Duration
	Log      *slog.Logger
	Now      func() time.Time
	wake     chan struct{}
}

func New(store *storage.Store, market Market, wfcd WFCD, cfg config.Config, log *slog.Logger) *Service {
	return &Service{Store: store, Market: market, WFCD: wfcd, Interval: cfg.CatalogInterval, Log: log, Now: time.Now, wake: make(chan struct{}, 1)}
}

func (s *Service) report(name string, err error) {
	s.Store.Report(name, err, s.Now())
	if err != nil {
		s.Log.Warn("refresh failed; keeping last good data", "worker", name, "error", err)
	}
}

// Run blocks until all workers stop, then flushes any final unpublished changes.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, run := range []func(context.Context){s.catalogues, s.statistics, s.publisher} {
		wg.Add(1)
		go func() { defer wg.Done(); run(ctx) }()
	}
	wg.Wait()
	if err := s.Store.PublishTradeable(s.Now()); err != nil {
		s.Log.Error("final publication failed", "error", err)
	}
}

func (s *Service) refreshKnowledge(ctx context.Context) error {
	release, err := s.WFCD.Latest(ctx)
	if err != nil {
		return err
	}
	if !s.Store.NeedsKnowledge(release.Version) {
		return nil
	}
	builder := normalize.NewKnowledge(s.Log)
	if err := s.WFCD.Snapshot(ctx, release, builder.AddSource); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	items, err := builder.Build()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Store.PublishKnowledge(items, release.Version); err != nil {
		return err
	}
	s.Log.Info("published knowledge", "version", release.Version, "items", len(items))
	return nil
}

func (s *Service) refreshMarket(ctx context.Context) error {
	items, err := s.Market.Items(ctx)
	if err != nil {
		return err
	}
	if err := s.Store.SetMarket(items, s.Now()); err != nil {
		return err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	s.Log.Info("published market catalogue", "items", len(items))
	return nil
}

func (s *Service) catalogues(ctx context.Context) {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		// A slow or unavailable WFCD source must not hold up the market catalogue.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); s.report("wfcd", s.refreshKnowledge(ctx)) }()
		go func() { defer wg.Done(); s.report("market_catalogue", s.refreshMarket(ctx)) }()
		wg.Wait()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Next selects only eligible work. Retry times prevent failed unseen listings
// from monopolizing the queue; all ties are deterministic.
func Next(items []storage.Candidate, retry map[string]time.Time, now time.Time) (*storage.Candidate, time.Duration) {
	var best *storage.Candidate
	var bestDue time.Time
	delay := time.Minute
	for _, item := range items {
		due := time.Time{}
		if item.LastFetchedAt != nil {
			due = item.LastFetchedAt.Add(normalize.TTL(item.Liquidity))
		}
		if retry[item.MarketID].After(due) {
			due = retry[item.MarketID]
		}
		if due.After(now) {
			if until := due.Sub(now); until < delay {
				delay = until
			}
			continue
		}
		prefer := best == nil
		if best != nil {
			switch {
			case (item.LastFetchedAt == nil) != (best.LastFetchedAt == nil):
				prefer = item.LastFetchedAt == nil
			case !due.Equal(bestDue):
				prefer = due.Before(bestDue)
			case item.Liquidity != best.Liquidity:
				prefer = item.Liquidity > best.Liquidity
			default:
				prefer = item.MarketID < best.MarketID
			}
		}
		if prefer {
			copy := item
			best = &copy
			bestDue = due
		}
	}
	return best, delay
}

func (s *Service) statistics(ctx context.Context) {
	retry := map[string]time.Time{}
	for ctx.Err() == nil {
		items, err := s.Store.Candidates(s.Now())
		if err != nil {
			s.report("statistics", err)
			if !wait(ctx, time.Minute, s.wake) {
				return
			}
			continue
		}
		item, delay := Next(items, retry, s.Now())
		if item == nil {
			if !wait(ctx, delay, s.wake) {
				return
			}
			continue
		}
		stats, err := s.Market.Statistics(ctx, item.Slug)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = s.Store.RecordStatistics(item.MarketID, stats, s.Now())
		}
		s.report("statistics", err)
		if err != nil {
			retry[item.MarketID] = s.Now().Add(5 * time.Minute)
		} else {
			delete(retry, item.MarketID)
		}
	}
}

func (s *Service) publisher(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		now := s.Now().UTC()
		nextDay := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		midnight := time.NewTimer(nextDay.Sub(now))
		select {
		case <-ctx.Done():
			midnight.Stop()
			return
		case <-ticker.C:
		case <-midnight.C:
		}
		midnight.Stop()
		s.report("publication", s.Store.PublishTradeable(s.Now()))
	}
}

func wait(ctx context.Context, delay time.Duration, wake <-chan struct{}) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}
