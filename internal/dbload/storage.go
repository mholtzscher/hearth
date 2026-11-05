package dbload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// WriteJSON writes only a caller-owned artifact; it does not create its parent.
func WriteJSON(path string, value any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	return errors.Join(encodeErr, f.Close())
}

// ReadManifest accepts a suite-owned closed fixture, never an arbitrary database.
func ReadManifest(path string) (Manifest, error) {
	var m Manifest
	if err := requireOwnedDirectory(filepath.Dir(path)); err != nil {
		return m, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.FormatVersion != FormatVersion {
		return m, errors.New("unsupported fixture format")
	}
	if err = ValidateScenario(m.Scenario); err != nil {
		return m, err
	}
	if !filepath.IsAbs(m.Database) {
		m.Database = filepath.Join(filepath.Dir(path), m.Database)
	}
	parent, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return m, err
	}
	database, err := filepath.Abs(m.Database)
	if err != nil {
		return m, err
	}
	if filepath.Dir(database) != parent {
		return m, errors.New("fixture database escapes owned directory")
	}
	info, err := os.Lstat(database)
	if err != nil || !info.Mode().IsRegular() {
		return m, errors.New("fixture must be a regular database file")
	}
	m.Database = database
	actual, err := fileHash(database)
	if err != nil {
		return m, err
	}
	if m.PhysicalHash != "" && actual != m.PhysicalHash {
		return m, errors.New("canonical database hash differs from manifest")
	}
	return m, nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func cloneFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

func diskBytes(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
}

func resourceCheck(path string, cfg Config, extra int64) error {
	owned, err := diskBytes(path)
	if err != nil {
		return err
	}
	if owned+extra > cfg.MaxDiskBytes {
		return fmt.Errorf(
			"resource limit: %d owned bytes + %d projected bytes exceeds %d",
			owned,
			extra,
			cfg.MaxDiskBytes,
		)
	}
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return err
	}
	//nolint:gosec // Linux statfs block counts fit int64 on supported hosts.
	free := int64(stat.Bavail) * stat.Bsize
	if free-extra < cfg.MinFreeBytes {
		return fmt.Errorf(
			"resource limit: free disk %d minus projected %d below reserve %d",
			free,
			extra,
			cfg.MinFreeBytes,
		)
	}
	return nil
}

func safeCaseName(name string) string {
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(name)
}
