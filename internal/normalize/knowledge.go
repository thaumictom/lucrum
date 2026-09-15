// Package normalize turns upstream data into the published contracts.
package normalize

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"strings"
)

type Document = map[string]any
type Knowledge map[string]Document

type definition struct {
	item     Document
	priority int
	origin   string
}

// Builder indexes source definitions first. Indexing never expands references
// or decides which records will be published.
type Builder struct {
	definitions map[string][]definition
	roots       map[string]bool
	log         *slog.Logger
	conflicts   int
}

func NewKnowledge(log *slog.Logger) *Builder {
	return &Builder{definitions: map[string][]definition{}, roots: map[string]bool{}, log: log}
}

// AddSource decodes one item at a time, allowing large category files to stream.
func (b *Builder) AddSource(filename string, r io.Reader) error {
	decoder := json.NewDecoder(r)
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return fmt.Errorf("expected an item array")
	}
	for index := 0; decoder.More(); index++ {
		var item Document
		if err := decoder.Decode(&item); err != nil {
			return err
		}
		if item == nil || identity(item) == "" {
			return fmt.Errorf("item %d has no uniqueName", index)
		}
		clean(item)
		b.index(item, "", fmt.Sprintf("%s/%09d", filename, index), true)
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func excluded(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	return key == "patchlog" || key == "patchlogs" || key == "wikiavailable"
}

func clean(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if excluded(key) {
				delete(v, key)
			} else {
				clean(child)
			}
		}
	case []any:
		for _, child := range v {
			clean(child)
		}
	}
}

func identity(item Document) string {
	if ref, ok := item["uniqueName"].(string); ok && ref != "" {
		return ref
	}
	ref, _ := item["gameRef"].(string)
	return ref
}

func dependency(field string) bool {
	switch field {
	case "components", "abilities", "requiredItems", "required_items", "requiredItem":
		return true
	}
	return false
}

func relationshipArray(field string) bool {
	switch field {
	case "components", "abilities", "requiredItems", "required_items", "drops",
		"rewards", "locations", "sources", "rewardSources", "exalted":
		return true
	}
	return false
}

func referenceField(field string) bool {
	return dependency(field) || field == "modSet" || field == "exalted" ||
		field == "item" || field == "source" || field == "sources" ||
		field == "rewardSource" || field == "rewardSources"
}

// splitContext MUST run before generic identity extraction. In particular, a
// drop's uniqueName identifies its source: chance/type/location describe the
// relationship, not that source's intrinsic properties.
func splitContext(object Document, field string) (Document, Document) {
	// Some relationship wrappers store the item under "item". Carry the
	// dependency context through the wrapper before looking for an identity.
	if dependency(field) && identity(object) == "" {
		if nested, ok := object["item"].(map[string]any); ok && identity(nested) != "" {
			item, edge := splitContext(nested, field)
			for _, key := range sortedKeys(object) {
				value := object[key]
				if key == "item" || excluded(key) {
					continue
				}
				if key == "itemCount" || key == "count" || key == "quantity" {
					continue
				}
				edge[key] = value
			}
			for _, key := range []string{"quantity", "itemCount", "count"} {
				if value, ok := object[key]; ok {
					edge["quantity"] = value
					break
				}
			}
			return item, edge
		}
	}
	item, edge := Document{}, Document{}
	if field == "drops" {
		if ref := identity(object); ref != "" {
			item["uniqueName"] = ref
		}
		for key, value := range object {
			if key != "uniqueName" && key != "gameRef" && !excluded(key) {
				edge[key] = value
			}
		}
		return item, edge
	}
	for key, value := range object {
		if excluded(key) {
			continue
		}
		isQuantity := key == "itemCount" || key == "quantity" || key == "count"
		isRequirement := key == "rank" || key == "requiredRank" || key == "minRank" || key == "consumed"
		if (field == "components" || field == "requiredItems" || field == "required_items" || field == "requiredItem") &&
			(isQuantity || isRequirement) {
			if !isQuantity {
				edge[key] = value
			}
			continue
		}
		if field == "rewards" && (key == "chance" || key == "rarity" || key == "rotation" || key == "location") {
			edge[key] = value
			continue
		}
		item[key] = value
	}
	if dependency(field) && field != "abilities" {
		for _, key := range []string{"quantity", "itemCount", "count"} {
			if value, ok := object[key]; ok {
				edge["quantity"] = value
				break
			}
		}
	}
	return item, edge
}

func (b *Builder) index(value any, field, origin string, top bool) {
	switch object := value.(type) {
	case map[string]any:
		// Classify first, even during the source-index pass.
		item, edge := splitContext(object, field)
		if ref := identity(item); ref != "" {
			priority := 1
			if top {
				priority = 0
			} else if len(item) <= 1 {
				priority = 2
			}
			b.definitions[ref] = append(b.definitions[ref], definition{item, priority, origin})
			if item["masterable"] == true || item["tradable"] == true {
				b.roots[ref] = true
			}
		}
		for _, key := range sortedKeys(item) {
			if key != "uniqueName" && key != "gameRef" {
				b.index(item[key], key, origin+"/"+key, false)
			}
		}
		for _, key := range sortedKeys(edge) {
			b.index(edge[key], key, origin+"/"+key, false)
		}
	case []any:
		for i, child := range object {
			b.index(child, field, fmt.Sprintf("%s/%09d", origin, i), false)
		}
	}
}

func sortedKeys(object Document) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func clone(value any) any {
	switch v := value.(type) {
	case map[string]any:
		copy := Document{}
		for key, child := range v {
			copy[key] = clone(child)
		}
		return copy
	case []any:
		copy := make([]any, len(v))
		for i, child := range v {
			copy[i] = clone(child)
		}
		return copy
	default:
		return value
	}
}

func jsonKey(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func uniqueEntries(values []any) []any {
	result := make([]any, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		key := jsonKey(value)
		if !seen[key] {
			result = append(result, value)
			seen[key] = true
		}
	}
	return result
}

func (b *Builder) merge(dst, src Document, ref string) {
	for _, key := range sortedKeys(src) {
		value := src[key]
		current, exists := dst[key]
		if !exists {
			dst[key] = clone(value)
			continue
		}
		if a, ok := current.(map[string]any); ok {
			if other, ok := value.(map[string]any); ok {
				b.merge(a, other, ref)
				continue
			}
		}
		if a, ok := current.([]any); ok {
			if other, ok := value.([]any); ok && relationshipArray(key) {
				dst[key] = uniqueEntries(append(a, clone(other).([]any)...))
			}
			// Other arrays, such as levelStats, are ordered authoritative values.
			continue
		}
		if !reflect.DeepEqual(current, value) {
			b.conflicts++
			if b.log != nil {
				b.log.Debug("WFCD field conflict; using preferred definition", "gameRef", ref, "field", key, "kept", current, "other", value)
			}
		}
	}
}

func (b *Builder) canonical(ref string) Document {
	defs := b.definitions[ref]
	sort.Slice(defs, func(i, j int) bool {
		if defs[i].priority != defs[j].priority {
			return defs[i].priority < defs[j].priority
		}
		return defs[i].origin < defs[j].origin
	})
	item := Document{"gameRef": ref}
	for _, def := range defs {
		b.merge(item, def.item, ref)
	}
	delete(item, "uniqueName")
	item["gameRef"] = ref
	return item
}

var displayFields = []string{"name", "description", "category", "type", "imageName", "wikiaThumbnail", "wikiaUrl", "wikiLink", "icon", "thumb"}

// Build expands only roots and dependencies. Associations are scalar display
// projections, so none of their outbound references enter the work queue.
func (b *Builder) Build() (Knowledge, error) {
	output := Knowledge{}
	full := map[string]bool{}
	expanded := map[string]bool{}
	queue := []string{}
	var ensure func(string, bool)
	ensure = func(ref string, expand bool) {
		if _, ok := output[ref]; !ok {
			display := Document{"gameRef": ref}
			source := b.canonical(ref)
			for _, key := range displayFields {
				if text, ok := source[key].(string); ok {
					display[key] = text
				}
			}
			output[ref] = display
		}
		if expand && !full[ref] {
			full[ref] = true
			queue = append(queue, ref)
		}
	}
	roots := make([]string, 0, len(b.roots))
	for ref := range b.roots {
		roots = append(roots, ref)
	}
	sort.Strings(roots)
	for _, ref := range roots {
		ensure(ref, true)
	}
	var visit func(any, string) any
	visit = func(value any, field string) any {
		switch object := value.(type) {
		case map[string]any:
			item, edge := splitContext(object, field)
			if ref := identity(item); ref != "" {
				ensure(ref, dependency(field))
				result := Document{"gameRef": ref}
				for _, key := range sortedKeys(edge) {
					result[key] = visit(edge[key], key)
				}
				return result
			}
			result := Document{}
			// With no identifier, a drop/reward wrapper remains ordinary metadata.
			for _, key := range sortedKeys(object) {
				if key != "uniqueName" && !excluded(key) {
					result[key] = visit(object[key], key)
				}
			}
			return result
		case []any:
			result := make([]any, 0, len(object))
			for _, child := range object {
				result = append(result, visit(child, field))
			}
			if relationshipArray(field) {
				result = uniqueEntries(result)
			}
			return result
		case string:
			if referenceField(field) && strings.HasPrefix(object, "/") {
				ensure(object, dependency(field))
				return Document{"gameRef": object}
			}
		}
		return value
	}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if expanded[ref] {
			continue
		}
		expanded[ref] = true
		source := b.canonical(ref)
		record := Document{"gameRef": ref}
		for _, key := range sortedKeys(source) {
			if key != "gameRef" && key != "uniqueName" && !excluded(key) {
				record[key] = visit(source[key], key)
			}
		}
		output[ref] = record
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("WFCD snapshot has no relevant items")
	}
	if err := ValidateKnowledge(output); err != nil {
		return nil, err
	}
	if b.log != nil && b.conflicts > 0 {
		b.log.Info("resolved WFCD field conflicts", "count", b.conflicts)
	}
	return output, nil
}

// ValidateKnowledge checks identities and all emitted references before publication.
func ValidateKnowledge(items Knowledge) error {
	var check func(any, bool) error
	check = func(value any, root bool) error {
		switch object := value.(type) {
		case map[string]any:
			for key, child := range object {
				if key == "uniqueName" || excluded(key) {
					return fmt.Errorf("forbidden field %s", key)
				}
				if err := check(child, false); err != nil {
					return err
				}
			}
			if ref, ok := object["gameRef"].(string); ok {
				if ref == "" || items[ref] == nil {
					return fmt.Errorf("unresolved reference %q", ref)
				}
				if !root {
					// These are intrinsic fields, never relationship annotations.
					for _, key := range []string{"name", "description", "components", "abilities", "drops", "masterable", "tradable", "imageName", "levelStats"} {
						if _, exists := object[key]; exists {
							return fmt.Errorf("embedded item field %s on %s", key, ref)
						}
					}
				}
			}
		case []any:
			for _, child := range object {
				if err := check(child, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for ref, item := range items {
		if ref == "" || item["gameRef"] != ref {
			return fmt.Errorf("invalid canonical identity %q", ref)
		}
		if err := check(item, true); err != nil {
			return err
		}
	}
	return nil
}
