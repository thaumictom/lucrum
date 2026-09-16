package warframedata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Like Record<string, unknown> in TypeScript, with the values kept as JSON
// bytes so unfamiliar fields and large numbers survive without conversion.
type object map[string]json.RawMessage

type catalogue struct {
	entries map[string]object
	roots   map[string]bool
}

func newCatalogue() *catalogue {
	return &catalogue{entries: make(map[string]object), roots: make(map[string]bool)}
}

// Decode one array element at a time. In particular, we do not hold all of a
// large category file in memory just to discard its non-tradable entries.
func (c *catalogue) read(reader io.Reader) error {
	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('[') {
		return errors.New("category JSON must be an array")
	}
	for decoder.More() {
		var item object
		if err := decoder.Decode(&item); err != nil {
			return err
		}
		if !bytes.Equal(item["tradable"], []byte("true")) && !bytes.Equal(item["masterable"], []byte("true")) {
			continue
		}
		if _, err := c.add(item, true); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil { // Closing array bracket.
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected content after category array")
	}
	return nil
}

func (c *catalogue) add(item object, root bool) (string, error) {
	var key string
	if err := json.Unmarshal(item["uniqueName"], &key); err != nil || key == "" {
		return "", errors.New("item or component has no valid uniqueName")
	}
	if raw, exists := item["components"]; exists {
		var components []object
		if err := json.Unmarshal(raw, &components); err != nil {
			return "", fmt.Errorf("components of %s: %w", key, err)
		}
		references := make([]string, 0, len(components))
		for _, component := range components {
			// Keep every component of a retained item, even when that component
			// isn't tradable/masterable itself. Otherwise references would dangle.
			name, err := c.add(component, false)
			if err != nil {
				return "", fmt.Errorf("component of %s: %w", key, err)
			}
			references = append(references, name)
		}
		raw, err := json.Marshal(references)
		if err != nil {
			return "", err
		}
		item["components"] = raw
	}
	delete(item, "uniqueName")
	// Prefer top-level fields, then the first component definition. Fill missing
	// fields and merge component links: an earlier copy may omit a sub-recipe.
	if previous, exists := c.entries[key]; exists {
		primary, secondary := previous, item
		if root && !c.roots[key] {
			primary, secondary = item, previous
		}
		if err := merge(primary, secondary); err != nil {
			return "", err
		}
		item = primary
	}
	c.entries[key] = item
	if root {
		c.roots[key] = true
	}
	return key, nil
}

func merge(primary, secondary object) error {
	for field, value := range secondary {
		if _, exists := primary[field]; !exists {
			primary[field] = value
		}
	}
	if primary["components"] == nil || secondary["components"] == nil {
		return nil
	}
	var references, additional []string
	if err := json.Unmarshal(primary["components"], &references); err != nil {
		return err
	}
	if err := json.Unmarshal(secondary["components"], &additional); err != nil {
		return err
	}
	seen := make(map[string]bool, len(references))
	for _, reference := range references {
		seen[reference] = true
	}
	for _, reference := range additional {
		if !seen[reference] {
			references = append(references, reference)
			seen[reference] = true
		}
	}
	raw, err := json.Marshal(references)
	if err != nil {
		return err
	}
	primary["components"] = raw
	return nil
}

func validateSaved(body []byte) error {
	var entries map[string]object
	if err := json.Unmarshal(body, &entries); err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("items must be a nonempty map keyed by uniqueName")
	}
	for key, item := range entries {
		if key == "" || item == nil {
			return errors.New("invalid flattened item")
		}
		if _, exists := item["uniqueName"]; exists {
			return fmt.Errorf("flattened item %s still contains uniqueName", key)
		}
		if raw, exists := item["components"]; exists {
			var references []string
			if err := json.Unmarshal(raw, &references); err != nil {
				return fmt.Errorf("invalid component references in %s: %w", key, err)
			}
			for _, reference := range references {
				if _, exists := entries[reference]; !exists {
					return fmt.Errorf("missing component %s referenced by %s", reference, key)
				}
			}
		}
	}
	return nil
}
