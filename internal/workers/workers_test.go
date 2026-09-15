package workers

import (
	"testing"
	"time"

	"github.com/thaumictom/lucrum/internal/storage"
)

func TestSchedulingPriorityRetryAndBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	items := []storage.Candidate{
		{MarketID: "hot", LastFetchedAt: &old, Liquidity: 151},
		{MarketID: "new"},
		{MarketID: "cold", LastFetchedAt: &old, Liquidity: 20},
	}
	next, _ := Next(items, nil, now)
	if next == nil || next.MarketID != "new" {
		t.Fatal(next)
	}
	next, _ = Next(items, map[string]time.Time{"new": now.Add(5 * time.Minute)}, now)
	if next == nil || next.MarketID != "hot" {
		t.Fatal(next)
	}
	for _, test := range []struct {
		liquidity int64
		hours     int
	}{{20, 24}, {21, 6}, {150, 6}, {151, 1}} {
		fetched := now.Add(-time.Duration(test.hours) * time.Hour)
		item := []storage.Candidate{{MarketID: "boundary", LastFetchedAt: &fetched, Liquidity: test.liquidity}}
		if next, _ := Next(item, nil, now.Add(-time.Nanosecond)); next != nil {
			t.Fatal("refreshed before TTL")
		}
		if next, _ := Next(item, nil, now); next == nil {
			t.Fatal("missed exact TTL")
		}
	}
}
