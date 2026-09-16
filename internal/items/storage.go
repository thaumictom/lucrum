package items

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"lucrum/internal/snapshot"
)

type Store struct {
	// Embedding exposes File's Read and ServeHTTP methods on Store too.
	// Unlike TS inheritance, this simply forwards these methods to the field.
	*snapshot.File
	dir        string
	sourceHash string // Only the catalogue worker changes this after startup.
	Changes    chan struct{}
}

type metadata struct {
	SourceHash string `json:"source_hash"`
	FileHash   string `json:"file_hash"`
}

// Entry is the small part of the catalogue that statistics processing needs.
type Entry struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	GameRef string `json:"gameRef"`
}

func NewStore(dir string) (*Store, error) {
	file, err := snapshot.New(dir, "wfm-items.json", validateSaved)
	if err != nil {
		return nil, err
	}
	s := &Store{File: file, dir: dir, Changes: make(chan struct{}, 1)}
	if !file.Available() {
		return s, nil
	}
	body, err := file.Read()
	if err != nil {
		return nil, err
	}
	metaBody, err := os.ReadFile(filepath.Join(dir, "wfm-items.meta.json"))
	var saved metadata
	if err == nil && json.Unmarshal(metaBody, &saved) == nil && saved.FileHash == snapshot.Hash(body) {
		s.sourceHash = saved.SourceHash
	} else {
		slog.Warn("catalogue metadata missing or inconsistent; will rebuild on refresh")
	}
	return s, nil
}

func (s *Store) Catalogue() ([]Entry, error) {
	body, err := s.Read()
	if err != nil {
		return nil, err
	}
	var catalogue struct {
		Items []Entry `json:"items"`
	}
	if err := json.Unmarshal(body, &catalogue); err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(catalogue.Items))
	for _, entry := range catalogue.Items {
		if entry.Slug == "" || entry.Name == "" || seen[entry.Slug] {
			return nil, errors.New("catalogue needs unique slugs and nonempty names")
		}
		seen[entry.Slug] = true
	}
	return catalogue.Items, nil
}

func validateSaved(body []byte) error {
	var saved document
	if err := json.Unmarshal(body, &saved); err != nil {
		return err
	}
	if saved.Items == nil || saved.LastFetchedAt.IsZero() {
		return errors.New("saved file needs items and last_fetched_at")
	}
	for index, entry := range saved.Items {
		var name string
		if err := json.Unmarshal(entry["name"], &name); err != nil || strings.TrimSpace(name) == "" {
			return fmt.Errorf("saved item %d has no valid name", index)
		}
		if _, exists := entry["id"]; exists {
			return fmt.Errorf("saved item %d still has id", index)
		}
		if _, exists := entry["i18n"]; exists {
			return fmt.Errorf("saved item %d still has i18n", index)
		}
	}
	return nil
}

func (s *Store) publish(body []byte, sourceHash string) error {
	if err := s.Publish(body); err != nil {
		return err
	}
	s.sourceHash = sourceHash
	metaBody, err := json.Marshal(metadata{SourceHash: sourceHash, FileHash: snapshot.Hash(body)})
	if err == nil {
		err = snapshot.Write(filepath.Join(s.dir, "wfm-items.meta.json"), metaBody)
	}
	if err != nil {
		slog.Warn("catalogue published but metadata could not be saved", "error", err)
	}
	// A buffered notification coalesces updates; consumers read the latest file.
	select {
	case s.Changes <- struct{}{}:
	default:
	}
	return nil
}
