// Package warframedata imports release-pinned WFCD data independently of WFM.
package warframedata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lucrum/internal/snapshot"
)

const repositoryAPI = "https://api.github.com/repos/WFCD/warframe-items"

type Service struct {
	*snapshot.File
	client   *http.Client
	metaPath string
	version  string
}

type metadata struct {
	Version  string `json:"version"`
	FileHash string `json:"file_hash"`
}

type sourceFile struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	DownloadURL string `json:"download_url"`
}

func New(dir string) (*Service, error) {
	file, err := snapshot.New(dir, "items.json", validateSaved)
	if err != nil {
		return nil, err
	}
	s := &Service{File: file, client: &http.Client{Timeout: 60 * time.Second}, metaPath: filepath.Join(dir, "items.meta.json")}
	if !file.Available() {
		return s, nil
	}
	body, err := file.Read()
	if err != nil {
		return nil, err
	}
	metaBody, err := os.ReadFile(s.metaPath)
	var saved metadata
	if err == nil && json.Unmarshal(metaBody, &saved) == nil && saved.FileHash == snapshot.Hash(body) {
		s.version = saved.Version
	} else {
		// A stop between the public file and metadata writes is harmless: serve
		// the validated snapshot, then rebuild without trusting the old version.
		slog.Warn("WFCD metadata missing or inconsistent; will rebuild")
	}
	return s, nil
}

// Run starts alongside the WFM catalogue worker and uses the same configured
// interval. A failed WFM request does not prevent this independent refresh.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.refresh(ctx); err != nil && ctx.Err() == nil {
			slog.Error("WFCD refresh failed; retaining latest valid snapshot", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) refresh(ctx context.Context) error {
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := s.getJSON(ctx, repositoryAPI+"/releases/latest", &release); err != nil {
		return fmt.Errorf("check WFCD release: %w", err)
	}
	if release.Tag == "" {
		return errors.New("GitHub release has no tag_name")
	}
	if release.Tag == s.version && s.Available() {
		slog.Debug("WFCD import skipped", "reason", "release unchanged", "version", release.Tag)
		return nil
	}

	// Use the release tag, never master: all files must describe the version
	// recorded in metadata, even if the repository changes during the import.
	var files []sourceFile
	if err := s.getJSON(ctx, repositoryAPI+"/contents/data/json?ref="+url.QueryEscape(release.Tag), &files); err != nil {
		return fmt.Errorf("list WFCD release files: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	catalogue := newCatalogue()
	count := 0
	for _, file := range files {
		if file.Type != "file" || !strings.HasSuffix(file.Name, ".json") || file.Name == "i18n.json" {
			slog.Debug("WFCD file skipped", "file", file.Name)
			continue
		}
		if file.DownloadURL == "" {
			return fmt.Errorf("WFCD file %s has no download URL", file.Name)
		}
		if err := s.readCategory(ctx, file.DownloadURL, catalogue); err != nil {
			return fmt.Errorf("import %s: %w", file.Name, err)
		}
		count++
	}
	if count == 0 || len(catalogue.entries) == 0 {
		return errors.New("WFCD release contained no usable items")
	}
	body, err := json.Marshal(catalogue.entries)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Publish(body); err != nil {
		return err
	}
	metaBody, err := json.Marshal(metadata{Version: release.Tag, FileHash: snapshot.Hash(body)})
	if err == nil {
		err = snapshot.Write(s.metaPath, metaBody)
	}
	if err != nil {
		return fmt.Errorf("snapshot published but could not save WFCD version: %w", err)
	}
	s.version = release.Tag
	slog.Info("WFCD items published", "version", release.Tag, "files", count, "items", len(catalogue.entries), "bytes", len(body))
	return nil
}

func (s *Service) getJSON(ctx context.Context, endpoint string, destination any) error {
	response, err := s.get(ctx, endpoint)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	const maxBytes = 2 << 20 // Release metadata and directory listings are small.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxBytes {
		return errors.New("GitHub metadata exceeds 2 MiB")
	}
	return json.Unmarshal(body, destination)
}

func (s *Service) readCategory(ctx context.Context, endpoint string, catalogue *catalogue) error {
	response, err := s.get(ctx, endpoint)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	// No source files are saved to disk. Filtered-out records are discarded as
	// we decode them, and only the final merged snapshot is persisted.
	const maxBytes = 64 << 20
	reader := &io.LimitedReader{R: response.Body, N: maxBytes + 1}
	if err := catalogue.read(reader); err != nil {
		return err
	}
	if reader.N <= 0 {
		return errors.New("WFCD category exceeds 64 MiB")
	}
	slog.Debug("WFCD category imported", "url", endpoint)
	return nil
}

func (s *Service) get(ctx context.Context, endpoint string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "lucrum")
	started := time.Now()
	slog.Debug("WFCD fetch started", "url", endpoint)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	slog.Debug("WFCD fetch response", "url", endpoint, "status", response.StatusCode, "duration", time.Since(started))
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("GitHub returned %s", response.Status)
	}
	return response, nil
}
