// Package snapshot publishes JSON files and serves consistent file/ETag pairs.
package snapshot

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type File struct {
	path string
	// HTTP handlers run concurrently. This lock protects the brief switch from
	// one file/ETag pair to the next, not the duration of a client's download.
	mu   sync.RWMutex
	etag string
}

func New(dir, name string, validate func([]byte) error) (*File, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	file := &File{path: filepath.Join(dir, name)}
	body, err := os.ReadFile(file.path)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return nil, err
	}
	if err := validate(body); err != nil {
		slog.Warn("saved snapshot is invalid; waiting for fresh data", "file", name, "error", err)
		return file, nil
	}
	file.etag = `"` + Hash(body) + `"`
	return file, nil
}

func Hash(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func (f *File) Available() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.etag != ""
}

func (f *File) Read() ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.etag == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(f.path)
}

func (f *File) Publish(body []byte) error {
	etag := `"` + Hash(body) + `"`
	f.mu.RLock()
	unchanged := f.etag == etag
	f.mu.RUnlock()
	if unchanged {
		return nil
	}
	path, err := prepare(f.path, body)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.Rename(path, f.path); err != nil {
		return err
	}
	f.etag = etag
	return nil
}

func (f *File) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f.mu.RLock()
	etag := f.etag
	if etag == "" {
		f.mu.RUnlock()
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "items are not available yet", http.StatusServiceUnavailable)
		return
	}
	file, err := os.Open(f.path)
	f.mu.RUnlock()
	if err != nil {
		slog.Error("open snapshot", "error", err)
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "items are not available", http.StatusServiceUnavailable)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, filepath.Base(f.path), time.Time{}, file)
}

// Write also gives private metadata an atomic replacement, without HTTP state.
func Write(path string, body []byte) error {
	temp, err := prepare(path, body)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	return os.Rename(temp, path)
}

// Named return values let deferred cleanup inspect the final error, similar
// to using a finally block to remove an unfinished temporary file in TS.
func prepare(destination string, body []byte) (path string, err error) {
	file, err := os.CreateTemp(filepath.Dir(destination), ".snapshot-*")
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
