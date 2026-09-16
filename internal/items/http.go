package items

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/warframe/v2/wfm-items" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	etag := s.etag
	if etag == "" {
		s.mu.RUnlock()
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "items are not available yet", http.StatusServiceUnavailable)
		return
	}
	file, err := os.Open(filepath.Join(s.dir, fileName))
	s.mu.RUnlock()
	if err != nil {
		slog.Error("open items for request", "error", err)
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "items are not available", http.StatusServiceUnavailable)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("ETag", etag)
	// ServeContent streams from disk and handles HEAD, ranges, and conditional
	// requests. A zero modification time makes the ETag our cache validator.
	http.ServeContent(w, r, fileName, time.Time{}, file)
}
