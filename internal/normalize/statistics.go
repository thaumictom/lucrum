package normalize

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/thaumictom/lucrum/internal/models"
)

var variantFields = []string{"mod_rank", "rank", "amber_stars", "cyan_stars", "charges", "subtype", "sub_type"}

func variant(row models.Row) models.Row {
	result := models.Row{}
	for _, key := range variantFields {
		if value, ok := row[key]; ok {
			result[key] = value
		}
	}
	return result
}

func stamp(row models.Row) (time.Time, error) {
	var text string
	if err := json.Unmarshal(row["datetime"], &text); err != nil {
		return time.Time{}, fmt.Errorf("invalid statistics datetime: %w", err)
	}
	return time.Parse(time.RFC3339Nano, text)
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

func volume(row models.Row) (int64, error) {
	var amount int64
	if err := json.Unmarshal(row["volume"], &amount); err != nil || amount < 0 {
		return 0, fmt.Errorf("closed statistics volume must be a nonnegative integer")
	}
	return amount, nil
}

// Retain keeps timestamps privately so the same rows can be rebucketed later.
func Retain(stats models.Statistics, now time.Time) (models.Statistics, error) {
	result := models.Statistics{Closed: []models.Row{}, Live: []models.Row{}}
	today, yesterday := day(now), day(now.UTC().AddDate(0, 0, -1))
	for _, row := range stats.Closed {
		at, err := stamp(row)
		if err != nil {
			return result, err
		}
		if _, err := volume(row); err != nil {
			return result, err
		}
		date := day(at.Add(24 * time.Hour))
		if date == today || date == yesterday {
			result.Closed = append(result.Closed, row)
		}
	}
	type latestRow struct {
		time time.Time
		row  models.Row
	}
	latest := map[string]latestRow{}
	for _, row := range stats.Live {
		at, err := stamp(row)
		if err != nil {
			return result, err
		}
		var orderType string
		if err := json.Unmarshal(row["order_type"], &orderType); err != nil {
			return result, fmt.Errorf("invalid live order_type")
		}
		at = at.Add(time.Hour)
		if orderType != "sell" || at.After(now) {
			continue
		}
		key := jsonKey(variant(row))
		prev, exists := latest[key]
		if !exists || at.After(prev.time) || (at.Equal(prev.time) && jsonKey(row) < jsonKey(prev.row)) {
			latest[key] = latestRow{at, row}
		}
	}
	for _, last := range latest {
		result.Live = append(result.Live, last.row)
	}
	for _, rows := range [][]models.Row{result.Closed, result.Live} {
		sort.Slice(rows, func(i, j int) bool { return jsonKey(rows[i]) < jsonKey(rows[j]) })
	}
	return result, nil
}

func publicRow(row models.Row) models.Row {
	result := models.Row{}
	for key, value := range row {
		if key != "datetime" && key != "id" {
			result[key] = value
		}
	}
	return result
}

func Tradeable(item models.MarketItem, cache *models.Cache, now time.Time) (models.TradeableItem, error) {
	result := models.TradeableItem{
		MarketID: item.MarketID, GameRef: item.GameRef, Slug: item.Slug, Name: item.Name,
		Variants: []models.Row{}, StatisticsToday: []models.Row{}, StatisticsYesterday: []models.Row{}, StatisticsLive: []models.Row{},
	}
	if cache == nil {
		return result, nil
	}
	rows, err := Retain(models.Statistics{Closed: cache.Closed, Live: cache.Live}, now)
	if err != nil {
		return result, err
	}
	fetched := cache.LastFetchedAt
	result.LastFetchedAt = &fetched
	variants := map[string]models.Row{}
	for _, row := range rows.Closed {
		at, _ := stamp(row)
		amount, _ := volume(row)
		if result.Liquidity > math.MaxInt64-amount {
			return result, fmt.Errorf("liquidity overflow")
		}
		result.Liquidity += amount
		if day(at.Add(24*time.Hour)) == day(now) {
			result.StatisticsToday = append(result.StatisticsToday, publicRow(row))
		} else {
			result.StatisticsYesterday = append(result.StatisticsYesterday, publicRow(row))
		}
		v := variant(row)
		variants[jsonKey(v)] = v
	}
	for _, row := range rows.Live {
		result.StatisticsLive = append(result.StatisticsLive, publicRow(row))
		v := variant(row)
		variants[jsonKey(v)] = v
	}
	keys := make([]string, 0, len(variants))
	for key := range variants {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.Variants = append(result.Variants, variants[key])
	}
	return result, nil
}

func TTL(liquidity int64) time.Duration {
	if liquidity <= 20 {
		return 24 * time.Hour
	}
	if liquidity <= 150 {
		return 6 * time.Hour
	}
	return time.Hour
}
