package httpapi

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thaumictom/lucrum/internal/normalize"
	"github.com/thaumictom/lucrum/internal/storage"
)

type countingReader struct {
	*strings.Reader
	reads int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}

func TestUnchangedChecksNeverReadContent(t *testing.T) {
	info := storage.FileInfo{ETag: `"known"`, ModTime: time.Now()}
	for _, method := range []string{"GET", "HEAD"} {
		content := &countingReader{Reader: strings.NewReader(`{"large":"dataset"}`)}
		request := httptest.NewRequest(method, "/api/items", nil)
		request.Header.Set("If-None-Match", info.ETag)
		response := httptest.NewRecorder()
		serveSnapshot(response, request, storage.ItemsFile, info, content)
		if response.Code != 304 || content.reads != 0 || response.Body.Len() != 0 {
			t.Fatalf("%s read content during revalidation", method)
		}
	}
	content := &countingReader{Reader: strings.NewReader(`{"large":"dataset"}`)}
	response := httptest.NewRecorder()
	serveSnapshot(response, httptest.NewRequest("HEAD", "/api/items", nil), storage.ItemsFile, info, content)
	if response.Code != 200 || content.reads != 0 || response.Body.Len() != 0 {
		t.Fatal("HEAD read file content")
	}
}

func TestConditionalRequestsAndAvailability(t *testing.T) {
	store, err := storage.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store)
	request := func(method, path string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request("GET", "/api/items", nil); got.Code != 503 {
		t.Fatal(got.Code)
	}
	if got := request("GET", "/healthz", nil); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := request("GET", "/readyz", nil); got.Code != 503 {
		t.Fatal(got.Code)
	}
	if err := store.PublishKnowledge(normalize.Knowledge{"/a": {"gameRef": "/a", "name": "A"}}, "v"); err != nil {
		t.Fatal(err)
	}
	get := request("GET", "/api/items", nil)
	etag, modified := get.Header().Get("ETag"), get.Header().Get("Last-Modified")
	if get.Code != 200 || etag == "" || modified == "" || get.Header().Get("Cache-Control") != "public, no-cache" {
		t.Fatal(get)
	}
	for _, condition := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		result := request("GET", "/api/items", map[string]string{"If-None-Match": condition})
		if result.Code != 304 || result.Body.Len() != 0 || result.Header().Get("ETag") != etag {
			t.Fatalf("%s: %v", condition, result)
		}
	}
	head := request("HEAD", "/api/items", nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != get.Header().Get("Content-Length") {
		t.Fatal(head)
	}
	if got := request("GET", "/api/items", map[string]string{"If-Modified-Since": modified}); got.Code != 304 {
		t.Fatal(got)
	}
	precedence := request("GET", "/api/items", map[string]string{"If-None-Match": `"stale"`, "If-Modified-Since": modified})
	if precedence.Code != 200 || precedence.Body.Len() == 0 {
		t.Fatal("date incorrectly overrode ETag")
	}
	if err := store.PublishKnowledge(normalize.Knowledge{"/a": {"gameRef": "/a", "name": "Changed"}}, "v2"); err != nil {
		t.Fatal(err)
	}
	changed := request("GET", "/api/items", map[string]string{"If-None-Match": etag})
	if changed.Code != 200 || changed.Header().Get("ETag") == etag {
		t.Fatal("stale validator did not receive new content")
	}
}
