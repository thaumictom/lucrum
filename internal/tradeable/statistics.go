package tradeable

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// Unknown statistics fields stay as raw JSON, like a TS Record whose values
// we pass through without needing to model every possible variant.
type row map[string]json.RawMessage

type Item struct {
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	GameRef       string     `json:"gameRef"`
	Liquidity     int64      `json:"liquidity"`
	Today         []row      `json:"statistics_today"`
	Yesterday     []row      `json:"statistics_yesterday"`
	Live          []row      `json:"statistics_live"`
	LastFetchedAt *time.Time `json:"last_fetched_at"` // nil encodes as JSON null.
}

type document struct {
	Items []Item `json:"items"`
}

func interval(liquidity int64) time.Duration {
	if liquidity > 150 {
		return time.Hour
	}
	if liquidity > 20 {
		return 6 * time.Hour
	}
	return 24 * time.Hour
}

func transform(body []byte, base Item, started time.Time) (Item, error) {
	var response struct {
		Payload struct {
			Closed struct {
				Days []row `json:"90days"`
			} `json:"statistics_closed"`
			Live struct {
				Hours []row `json:"48hours"`
			} `json:"statistics_live"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Item{}, err
	}
	if response.Payload.Closed.Days == nil || response.Payload.Live.Hours == nil {
		return Item{}, errors.New("statistics response must contain closed 90days and live 48hours arrays")
	}
	base.Today, base.Yesterday, base.Live = []row{}, []row{}, []row{}
	base.Liquidity = 0
	// WFM labels completed periods one day behind. These names describe the
	// request's snapshot, not a rolling view that changes at our next midnight.
	today := started.UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	yesterday := started.UTC().AddDate(0, 0, -2).Format(time.DateOnly)
	for _, record := range response.Payload.Closed.Days {
		date, err := timestamp(record)
		if err != nil {
			return Item{}, err
		}
		day := date.UTC().Format(time.DateOnly)
		if day != today && day != yesterday {
			continue
		}
		var volume int64
		if err := json.Unmarshal(record["volume"], &volume); err != nil || string(record["volume"]) == "null" || volume < 0 || volume > math.MaxInt64-base.Liquidity {
			return Item{}, errors.New("invalid statistics volume")
		}
		base.Liquidity += volume
		strip(record)
		if day == today {
			base.Today = append(base.Today, record)
		} else {
			base.Yesterday = append(base.Yesterday, record)
		}
	}

	// The array can be grouped by order type or rank, rather than timestamp.
	// Find the newest sell timestamp, retaining every variant at that time.
	var latest time.Time
	for _, record := range response.Payload.Live.Hours {
		var orderType string
		if err := json.Unmarshal(record["order_type"], &orderType); err != nil {
			return Item{}, fmt.Errorf("invalid order_type: %w", err)
		}
		if orderType != "sell" {
			continue
		}
		date, err := timestamp(record)
		if err != nil {
			return Item{}, err
		}
		if date.After(latest) {
			latest = date
			base.Live = []row{}
		}
		if date.Equal(latest) {
			strip(record)
			base.Live = append(base.Live, record)
		}
	}
	base.LastFetchedAt = &started
	return base, nil
}

func timestamp(record row) (time.Time, error) {
	var date time.Time
	if err := json.Unmarshal(record["datetime"], &date); err != nil {
		return date, fmt.Errorf("invalid statistics datetime: %w", err)
	}
	if date.IsZero() {
		return date, errors.New("missing statistics datetime")
	}
	return date, nil
}

func strip(record row) {
	delete(record, "id")
	delete(record, "datetime")
}

func validateSaved(body []byte) error {
	var saved document
	if err := json.Unmarshal(body, &saved); err != nil {
		return err
	}
	if saved.Items == nil {
		return errors.New("tradeable snapshot needs an items array")
	}
	seen := make(map[string]bool, len(saved.Items))
	for _, item := range saved.Items {
		if item.Slug == "" || item.Name == "" || seen[item.Slug] || item.Liquidity < 0 || item.Today == nil || item.Yesterday == nil || item.Live == nil {
			return fmt.Errorf("invalid saved tradeable item %q", item.Slug)
		}
		seen[item.Slug] = true
	}
	return nil
}
