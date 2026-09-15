package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"time"

	"github.com/thaumictom/lucrum/internal/models"
)

type Market struct {
	HTTP    *HTTP
	BaseURL string
}

func NewMarket(rate float64) *Market {
	h := newHTTP(30 * time.Second)
	h.Market = true
	h.Limiter = NewLimiter(rate)
	return &Market{HTTP: h, BaseURL: "https://api.warframe.market"}
}

func decodeJSON(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func (m *Market) Items(ctx context.Context) ([]models.MarketItem, error) {
	response, err := m.HTTP.Get(ctx, m.BaseURL+"/v2/items")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var body struct {
		Data []struct {
			ID      string   `json:"id"`
			GameRef string   `json:"gameRef"`
			Slug    string   `json:"slug"`
			Tags    []string `json:"tags"`
			I18N    map[string]struct {
				Name string `json:"name"`
			} `json:"i18n"`
			models.Capabilities
		} `json:"data"`
	}
	if err := decodeJSON(response.Body, &body); err != nil {
		return nil, fmt.Errorf("decode market catalogue: %w", err)
	}
	if len(body.Data) == 0 {
		return nil, fmt.Errorf("market catalogue is missing or empty")
	}
	items := make([]models.MarketItem, 0, len(body.Data))
	seen := make(map[string]bool)
	for _, item := range body.Data {
		if item.ID == "" || item.Slug == "" || item.I18N["en"].Name == "" || seen[item.ID] {
			return nil, fmt.Errorf("invalid or duplicate market listing %q", item.ID)
		}
		seen[item.ID] = true
		tags := item.Tags
		if tags == nil {
			tags = []string{}
		}
		items = append(items, models.MarketItem{
			MarketID: item.ID, GameRef: item.GameRef, Slug: item.Slug,
			Name: item.I18N["en"].Name, Tags: tags, Capabilities: item.Capabilities,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].MarketID < items[j].MarketID })
	return items, nil
}

func (m *Market) Statistics(ctx context.Context, slug string) (models.Statistics, error) {
	var stats models.Statistics
	response, err := m.HTTP.Get(ctx, m.BaseURL+"/v1/items/"+url.PathEscape(slug)+"/statistics")
	if err != nil {
		return stats, err
	}
	defer response.Body.Close()
	var body struct {
		Payload struct {
			Closed map[string]json.RawMessage `json:"statistics_closed"`
			Live   map[string]json.RawMessage `json:"statistics_live"`
		} `json:"payload"`
	}
	if err := decodeJSON(response.Body, &body); err != nil {
		return stats, err
	}
	closed, live := body.Payload.Closed["90days"], body.Payload.Live["48hours"]
	if len(closed) == 0 || len(live) == 0 {
		return stats, fmt.Errorf("statistics response is missing required intervals")
	}
	if err := json.Unmarshal(closed, &stats.Closed); err != nil {
		return stats, err
	}
	if err := json.Unmarshal(live, &stats.Live); err != nil {
		return stats, err
	}
	if stats.Closed == nil || stats.Live == nil {
		return stats, fmt.Errorf("statistics intervals must be arrays")
	}
	return stats, nil
}
