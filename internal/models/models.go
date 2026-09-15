// Package models contains the small public market contracts and private cache format.
package models

import (
	"encoding/json"
	"time"
)

// Row keeps upstream statistics fields without rounding numbers or losing new fields.
type Row map[string]json.RawMessage

type Capabilities struct {
	MaxRank       *int     `json:"maxRank,omitempty"`
	MaxCharges    *int     `json:"maxCharges,omitempty"`
	MaxAmberStars *int     `json:"maxAmberStars,omitempty"`
	MaxCyanStars  *int     `json:"maxCyanStars,omitempty"`
	Subtypes      []string `json:"subtypes,omitempty"`
}

type MarketItem struct {
	MarketID string   `json:"market_id"`
	GameRef  string   `json:"gameRef"`
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags"`
	Capabilities
}

type Statistics struct {
	Closed []Row
	Live   []Row
}

// Cache is private: timestamps are needed to rebucket rows at UTC midnight.
type Cache struct {
	MarketID      string    `json:"market_id"`
	Closed        []Row     `json:"closed"`
	Live          []Row     `json:"live"`
	LastFetchedAt time.Time `json:"last_fetched_at"`
}

type TradeableItem struct {
	MarketID            string     `json:"market_id"`
	GameRef             string     `json:"gameRef"`
	Slug                string     `json:"slug"`
	Name                string     `json:"name"`
	Variants            []Row      `json:"variants"`
	StatisticsToday     []Row      `json:"statistics_today"`
	StatisticsYesterday []Row      `json:"statistics_yesterday"`
	StatisticsLive      []Row      `json:"statistics_live"`
	Liquidity           int64      `json:"liquidity"`
	LastFetchedAt       *time.Time `json:"last_fetched_at"`
}
