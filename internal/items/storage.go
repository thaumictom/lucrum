package items

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const fileName = "wfm-items.json"
const metadataName = "wfm-items.meta.json"

type Store struct {
	dir string

	// Unlike normal JS callbacks, Go HTTP handlers can run simultaneously.
	// This lock keeps file opening and its ETag consistent during replacement.
	mu   sync.RWMutex
	etag string

	// Only the single refresh worker accesses this after startup.
	sourceHash string
}

type metadata struct {
	SourceHash string `json:"source_hash"`
	FileHash   string `json:"file_hash"`
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	store := &Store{dir: dir}
	body, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read saved items: %w", err)
	}
	if err := validateSaved(body); err != nil {
		slog.Warn("saved file is invalid; waiting for a fresh fetch", "error", err)
		return store, nil
	}
	fileHash := hash(body)
	store.etag = `"` + fileHash + `"`

	metaBody, err := os.ReadFile(filepath.Join(dir, metadataName))
	var saved metadata
	if err == nil && json.Unmarshal(metaBody, &saved) == nil && saved.FileHash == fileHash {
		store.sourceHash = saved.SourceHash
	} else {
		// The process might have stopped between publishing the file and metadata.
		// The file is still usable; an empty source hash forces a fresh rebuild.
		slog.Warn("saved metadata missing or inconsistent; will rebuild on refresh")
	}
	slog.Info("loaded saved items", "bytes", len(body))
	return store, nil
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
	tempPath, err := writeTemp(s.dir, body)
	if err != nil {
		return fmt.Errorf("prepare items file: %w", err)
	}
	defer os.Remove(tempPath)
	fileHash := hash(body)

	// Write the slow part first; hold the lock only while replacing the file
	// and its ETag. Linux readers with an open old file can finish reading it.
	s.mu.Lock()
	err = os.Rename(tempPath, filepath.Join(s.dir, fileName))
	if err == nil {
		s.etag = `"` + fileHash + `"`
		s.sourceHash = sourceHash
	}
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("replace items file: %w", err)
	}

	metaBody, err := json.Marshal(metadata{SourceHash: sourceHash, FileHash: fileHash})
	if err == nil {
		var metaPath string
		metaPath, err = writeTemp(s.dir, metaBody)
		if err == nil {
			defer os.Remove(metaPath)
			err = os.Rename(metaPath, filepath.Join(s.dir, metadataName))
		}
	}
	if err != nil {
		// Publication succeeded. Missing metadata only costs a rebuild on restart.
		slog.Warn("items published but metadata could not be saved", "error", err)
	}
	return nil
}

// A temp file must be on the same filesystem for Linux rename to be atomic.
// Naming the return values lets the deferred cleanup inspect the final error.
func writeTemp(dir string, body []byte) (path string, err error) {
	file, err := os.CreateTemp(dir, ".wfm-*")
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		file.Close()
		if err != nil {
			os.Remove(path)
		}
	}()
	if _, err = file.Write(body); err != nil {
		return path, err
	}
	if err = file.Sync(); err != nil {
		return path, err
	}
	err = file.Close()
	return path, err
}
