package normalize

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/thaumictom/lucrum/internal/models"
)

func row(text string) models.Row {
	var result models.Row
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		panic(err)
	}
	return result
}

func TestStatisticsSelectionAndRollover(t *testing.T) {
	now := time.Date(2026, 9, 15, 15, 30, 0, 0, time.UTC)
	stats := models.Statistics{
		Closed: []models.Row{
			row(`{"datetime":"2026-09-14T00:00:00Z","id":"a","mod_rank":0,"volume":10,"new_number":9007199254740993,"precise":1.0000000000000000001}`),
			row(`{"datetime":"2026-09-14T00:00:00Z","id":"b","mod_rank":5,"volume":11}`),
			row(`{"datetime":"2026-09-13T00:00:00Z","mod_rank":0,"volume":30}`),
			row(`{"datetime":"2026-09-12T00:00:00Z","volume":999}`),
		},
		Live: []models.Row{
			row(`{"datetime":"2026-09-15T14:00:00Z","id":"new","mod_rank":0,"order_type":"sell","volume":4}`),
			row(`{"datetime":"2026-09-15T15:00:00Z","mod_rank":0,"order_type":"sell","volume":999}`),
			row(`{"datetime":"2026-09-15T13:00:00Z","mod_rank":5,"order_type":"sell","volume":7}`),
			row(`{"datetime":"2026-09-15T12:00:00Z","mod_rank":0,"order_type":"sell","volume":1}`),
			row(`{"datetime":"2026-09-15T14:00:00Z","mod_rank":5,"order_type":"buy","volume":888}`),
		},
	}
	retained, err := Retain(stats, now)
	if err != nil {
		t.Fatal(err)
	}
	cache := models.Cache{MarketID: "id", Closed: retained.Closed, Live: retained.Live, LastFetchedAt: now}
	item := models.MarketItem{MarketID: "id", GameRef: "/a", Slug: "a", Name: "A"}
	view, err := Tradeable(item, &cache, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.StatisticsToday) != 2 || len(view.StatisticsYesterday) != 1 || len(view.StatisticsLive) != 2 || view.Liquidity != 51 {
		t.Fatalf("wrong selection: %+v", view)
	}
	for _, rows := range [][]models.Row{view.StatisticsToday, view.StatisticsYesterday, view.StatisticsLive} {
		for _, r := range rows {
			if r["id"] != nil || r["datetime"] != nil {
				t.Fatal("private fields leaked")
			}
			if string(r["volume"]) == "999" || string(r["volume"]) == "888" {
				t.Fatal("future/buy row retained")
			}
		}
	}
	var precise models.Row
	for _, r := range view.StatisticsToday {
		if string(r["mod_rank"]) == "0" {
			precise = r
		}
	}
	if string(precise["new_number"]) != "9007199254740993" || string(precise["precise"]) != "1.0000000000000000001" {
		t.Fatal("statistics numeric precision changed")
	}
	next, err := Tradeable(item, &cache, time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(next.StatisticsToday) != 0 || len(next.StatisticsYesterday) != 2 || next.Liquidity != 21 || !next.LastFetchedAt.Equal(now) {
		t.Fatalf("wrong rollover: %+v", next)
	}
}

func TestVariantsOffsetsEmptyAndValidation(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	stats := models.Statistics{
		Closed: []models.Row{row(`{"datetime":"2025-12-30T22:00:00-02:00","volume":3}`)},
		Live: []models.Row{
			row(`{"datetime":"2025-12-31T23:00:00Z","order_type":"sell","amber_stars":0,"cyan_stars":0,"subtype":"regular","charges":0}`),
			row(`{"datetime":"2025-12-31T23:00:00Z","order_type":"sell","amber_stars":2,"cyan_stars":2,"subtype":"special","charges":1}`),
		},
	}
	cache := &models.Cache{Closed: stats.Closed, Live: stats.Live, LastFetchedAt: now}
	got, err := Tradeable(models.MarketItem{}, cache, now)
	if err != nil || len(got.StatisticsToday) != 1 || len(got.StatisticsLive) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	empty, err := Tradeable(models.MarketItem{}, nil, now)
	if err != nil || empty.LastFetchedAt != nil || empty.StatisticsToday == nil || empty.Variants == nil {
		t.Fatal(empty, err)
	}
	for _, bad := range []models.Row{row(`{"datetime":"bad","volume":1}`), row(`{"datetime":"2026-01-01T00:00:00Z","volume":-1}`)} {
		if _, err := Retain(models.Statistics{Closed: []models.Row{bad}}, now); err == nil {
			t.Fatal("accepted invalid row")
		}
	}
}

func TestTTLBoundaries(t *testing.T) {
	for _, test := range []struct {
		liquidity int64
		hours     int
	}{{0, 24}, {20, 24}, {21, 6}, {150, 6}, {151, 1}} {
		if got := TTL(test.liquidity); got != time.Duration(test.hours)*time.Hour {
			t.Errorf("%d: %v", test.liquidity, got)
		}
	}
}
