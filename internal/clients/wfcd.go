package clients

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

type Release struct {
	Version string `json:"version"`
	Dist    struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

type WFCD struct {
	HTTP        *HTTP
	RegistryURL string
}

func NewWFCD() *WFCD {
	return &WFCD{HTTP: newHTTP(3 * time.Minute), RegistryURL: "https://registry.npmjs.org/@wfcd/items/latest"}
}

func (w *WFCD) Latest(ctx context.Context) (Release, error) {
	var release Release
	response, err := w.HTTP.Get(ctx, w.RegistryURL)
	if err != nil {
		return release, err
	}
	defer response.Body.Close()
	if err := decodeJSON(response.Body, &release); err != nil {
		return release, err
	}
	if release.Version == "" || release.Dist.Tarball == "" {
		return release, fmt.Errorf("WFCD release is missing its version or archive")
	}
	return release, nil
}

// Snapshot streams category files to the normalizer. It never extracts archive
// paths onto disk and never runs anything from the npm package.
func (w *WFCD) Snapshot(ctx context.Context, release Release, consume func(string, io.Reader) error) error {
	response, err := w.HTTP.Get(ctx, release.Dist.Tarball)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	hash := sha512.New()
	body := io.TeeReader(response.Body, hash)
	compressed, err := gzip.NewReader(body)
	if err != nil {
		return err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := path.Base(header.Name)
		if header.Typeflag != tar.TypeReg || path.Dir(header.Name) != "package/data/json" ||
			!strings.HasSuffix(name, ".json") || name == "i18n.json" || name == "All.json" {
			continue
		}
		if err := consume(name, archive); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		count++
	}
	// Read through the gzip trailer so truncation and checksum errors are detected.
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, body); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("WFCD archive contains no category files")
	}
	if release.Dist.Integrity != "" {
		actual := "sha512-" + base64.StdEncoding.EncodeToString(hash.Sum(nil))
		if actual != release.Dist.Integrity {
			return fmt.Errorf("WFCD archive integrity mismatch")
		}
	}
	return nil
}
