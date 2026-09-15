// Package httpapi serves published snapshots. A request never contacts upstream.
package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/thaumictom/lucrum/internal/storage"
)

func New(store *storage.Store) http.Handler {
	mux := http.NewServeMux()
	for route, file := range map[string]string{
		"/api/items":                 storage.ItemsFile,
		"/api/warframe-market-items": storage.MarketFile,
		"/api/tradeable-items":       storage.TradeableFile,
	} {
		mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
			open, info, err := store.Open(file)
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dataset not available yet"})
				return
			}
			defer open.Close()
			serveSnapshot(w, r, file, info, open)
		})
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.Health())
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		health := store.Health()
		status := http.StatusOK
		if !health.Ready {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, health)
	})
	return mux
}

func serveSnapshot(w http.ResponseWriter, r *http.Request, name string, info storage.FileInfo, content io.ReadSeeker) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("ETag", info.ETag)
	// ServeContent handles HEAD, weak/list ETag matching, dates, and Range.
	// Matching conditional requests return before reading the file body.
	http.ServeContent(w, r, name, info.ModTime, content)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
