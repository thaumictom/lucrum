// Package storage owns durable cache state and complete published file snapshots.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type FileInfo struct {
	ETag    string
	ModTime time.Time
	Size    int64
}

type prepared struct {
	path string
	info FileInfo
}

func validateJSON(reader io.Reader) error {
	decoder := json.NewDecoder(reader)
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(&value); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func inspect(path string) (FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return FileInfo{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if err := validateJSON(io.TeeReader(file, hash)); err != nil {
		return FileInfo{}, err
	}
	stat, err := file.Stat()
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{ETag: `"` + hex.EncodeToString(hash.Sum(nil)) + `"`, ModTime: stat.ModTime(), Size: stat.Size()}, nil
}

// prepareJSON hashes the exact bytes being written; HTTP requests reuse that
// digest instead of reading the large file to calculate a validator.
func prepareJSON(dir string, value any) (result prepared, err error) {
	file, err := os.CreateTemp(dir, ".lucrum-*.tmp")
	if err != nil {
		return result, err
	}
	result.path = file.Name()
	defer func() {
		file.Close()
		if err != nil {
			os.Remove(result.path)
		}
	}()
	if err = file.Chmod(0644); err != nil {
		return result, err
	}
	hash := sha256.New()
	encoder := json.NewEncoder(io.MultiWriter(file, hash))
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(value); err != nil {
		return result, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	if err = validateJSON(file); err != nil {
		return result, err
	}
	if err = file.Sync(); err != nil {
		return result, err
	}
	stat, err := file.Stat()
	if err != nil {
		return result, err
	}
	result.info = FileInfo{ETag: `"` + hex.EncodeToString(hash.Sum(nil)) + `"`, ModTime: stat.ModTime(), Size: stat.Size()}
	err = file.Close()
	return result, err
}

func syncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

// atomicJSON is also used for private files. All live JSON uses the same write path.
func atomicJSON(path string, value any) error {
	temp, err := prepareJSON(filepath.Dir(path), value)
	if err != nil {
		return err
	}
	defer os.Remove(temp.path)
	if err := os.Rename(temp.path, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
