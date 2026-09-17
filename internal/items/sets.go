package items

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// linkSets reads the flattened component links without importing warframedata
// (which already depends on items). Atomic snapshots make this read consistent.
func (s *Store) linkSets(body []byte) ([]byte, error) {
	gameBody, err := os.ReadFile(filepath.Join(s.dir, "items.json"))
	if errors.Is(err, os.ErrNotExist) {
		return body, nil // The independent WFCD importer may not have finished yet.
	}
	if err != nil {
		return nil, fmt.Errorf("read item parents: %w", err)
	}
	var parents map[string]struct {
		Components []string `json:"components"`
	}
	if err := json.Unmarshal(gameBody, &parents); err != nil {
		return nil, fmt.Errorf("decode item parents: %w", err)
	}
	var catalogue document
	if err := json.Unmarshal(body, &catalogue); err != nil {
		return nil, err
	}
	byRef := make(map[string]string)
	for _, entry := range catalogue.Items {
		var ref, slug string
		if err := json.Unmarshal(entry["gameRef"], &ref); err != nil {
			continue
		}
		if err := json.Unmarshal(entry["slug"], &slug); err != nil {
			continue
		}
		if ref != "" && slug != "" && byRef[ref] == "" {
			byRef[ref] = slug
		}
	}
	marketSlug := func(key string) string {
		if slug := byRef[key]; slug != "" {
			return slug
		}
		if strings.HasSuffix(key, "Component") {
			return byRef[strings.TrimSuffix(key, "Component")+"Blueprint"]
		}
		return ""
	}
	sets := make(map[string]string)
	ambiguous := make(map[string]bool)
	for key, parent := range parents {
		setSlug := marketSlug(key)
		if setSlug == "" {
			continue
		}
		for _, component := range parent.Components {
			slug := marketSlug(component)
			if slug == "" || slug == setSlug {
				continue
			}
			if previous := sets[slug]; previous != "" && previous != setSlug {
				ambiguous[slug] = true
			}
			sets[slug] = setSlug
		}
	}
	for _, entry := range catalogue.Items {
		delete(entry, "set_slug")
		var tags []string
		if err := json.Unmarshal(entry["tags"], &tags); err != nil {
			continue
		}
		if !slices.Contains(tags, "component") && !slices.Contains(tags, "blueprint") {
			continue
		}
		var slug string
		if err := json.Unmarshal(entry["slug"], &slug); err != nil {
			continue
		}
		if setSlug := sets[slug]; setSlug != "" && !ambiguous[slug] {
			entry["set_slug"], _ = json.Marshal(setSlug)
		}
	}
	return json.Marshal(catalogue)
}
