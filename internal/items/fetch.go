package items

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"lucrum/internal/snapshot"
	"lucrum/internal/upstream"
)

const upstreamURL = "https://api.warframe.market/v2/items"

// RawMessage keeps each value as JSON bytes. This is similar to a TypeScript
// Record<string, unknown>, but preserves unknown fields and exact JSON numbers.
type item map[string]json.RawMessage

type document struct {
	Items         []item    `json:"items"`
	LastFetchedAt time.Time `json:"last_fetched_at"`
}

// Run uses one worker: refreshes never overlap, even if a request is slow.
func (s *Store) Run(ctx context.Context, interval time.Duration, client *upstream.Client) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := s.refresh(ctx, client); err != nil && ctx.Err() == nil {
			slog.Error("refresh failed; keeping the last published file", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) refresh(ctx context.Context, client *upstream.Client) error {
	body, fetchedAt, err := client.Fetch(ctx, upstreamURL, nil)
	if err != nil {
		return fmt.Errorf("fetch items: %w", err)
	}
	sourceHash := snapshot.Hash(body)
	if sourceHash == s.sourceHash {
		slog.Debug("catalogue rebuild skipped", "reason", "upstream unchanged")
		return nil
	}

	output, err := transform(body, fetchedAt)
	if err != nil {
		return err
	}
	if err := s.publish(output, sourceHash); err != nil {
		return err
	}
	slog.Info("items published", "bytes", len(output), "fetched_at", fetchedAt)
	return nil
}

func transform(body []byte, fetchedAt time.Time) ([]byte, error) {
	var upstream struct {
		Data []item `json:"data"`
	}
	if err := json.Unmarshal(body, &upstream); err != nil {
		return nil, fmt.Errorf("decode upstream JSON: %w", err)
	}
	if upstream.Data == nil {
		return nil, errors.New("upstream data must be an array")
	}
	for index, entry := range upstream.Data {
		var translations struct {
			English struct {
				Name string `json:"name"`
			} `json:"en"`
		}
		if err := json.Unmarshal(entry["i18n"], &translations); err != nil {
			return nil, fmt.Errorf("item %d: invalid i18n: %w", index, err)
		}
		if strings.TrimSpace(translations.English.Name) == "" {
			return nil, fmt.Errorf("item %d: missing English name", index)
		}
		name, err := json.Marshal(translations.English.Name)
		if err != nil {
			return nil, fmt.Errorf("item %d: encode name: %w", index, err)
		}
		delete(entry, "id")
		delete(entry, "i18n")
		entry["name"] = name
	}
	return json.Marshal(document{Items: upstream.Data, LastFetchedAt: fetchedAt})
}
