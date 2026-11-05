package dbload

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	FormatVersion             = 1
	DefaultDiskLimit    int64 = 8 << 30
	DefaultFreeDisk     int64 = 4 << 30
	DefaultRSSLimit     int64 = 8 << 30
	ConfirmationPairs         = 20
	ConfirmationSamples       = 200
	DefaultPageSize           = 50
	markerName                = ".hearth-dbload"
)

// Config describes one run; confirmation has stricter registered sample requirements.
type Config struct {
	OutputDir      string
	Fixture        string
	SourceRoot     string
	Cases          []string
	Samples        int
	Blocks         int
	Concurrency    int
	Variant        string
	Pair           int
	Order          string
	Budget         time.Duration
	CaseLimit      time.Duration
	OperationLimit time.Duration
	MaxDiskBytes   int64
	MinFreeBytes   int64
	MaxWorkerRSS   int64
	Profile        bool
}

// Defaults applies developer-tool defaults, never production database tuning.
func (cfg Config) Defaults() Config {
	if cfg.Samples == 0 {
		cfg.Samples = 20
	}
	if cfg.Blocks == 0 {
		cfg.Blocks = 3
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 1
	}
	if cfg.SourceRoot == "" {
		cfg.SourceRoot = "."
	}
	if cfg.Budget == 0 {
		cfg.Budget = 2 * time.Hour
	}
	if cfg.CaseLimit == 0 {
		cfg.CaseLimit = time.Minute
	}
	if cfg.OperationLimit == 0 {
		cfg.OperationLimit = 10 * time.Second
	}
	if cfg.MaxDiskBytes == 0 {
		cfg.MaxDiskBytes = DefaultDiskLimit
	}
	if cfg.MinFreeBytes == 0 {
		cfg.MinFreeBytes = DefaultFreeDisk
	}
	if cfg.MaxWorkerRSS == 0 {
		cfg.MaxWorkerRSS = DefaultRSSLimit
	}
	if cfg.Variant == "" {
		cfg.Variant = "baseline"
	}
	if cfg.Order == "" {
		cfg.Order = "AB"
	}
	return cfg
}

// Validate checks independently supplied run inputs before opening SQLite.
func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.OutputDir) == "" {
		return errors.New("a new output directory is required")
	}
	if cfg.Samples < 1 || cfg.Samples > 100000 || cfg.Blocks < 1 || cfg.Blocks > 100 {
		return errors.New("invalid sample or block count")
	}
	if cfg.Concurrency != 1 && cfg.Concurrency != 4 && cfg.Concurrency != 16 {
		return errors.New("concurrency must be 1, 4, or 16")
	}
	if cfg.Budget <= 0 || cfg.CaseLimit <= 0 || cfg.OperationLimit <= 0 || cfg.OperationLimit > cfg.CaseLimit {
		return errors.New("invalid execution limits")
	}
	if cfg.MaxDiskBytes < 1 || cfg.MinFreeBytes < 0 || cfg.MaxWorkerRSS < 1 {
		return errors.New("invalid resource limits")
	}
	if cfg.Order != "AB" && cfg.Order != "BA" {
		return errors.New("pair order must be AB or BA")
	}
	return nil
}

// ValidateScenario rejects meaningless multipliers and illegal compound definitions.
func ValidateScenario(s Scenario) error {
	if s.Scale != 1 && s.Scale != 10 && s.Scale != 100 && s.Scale != 1000 {
		return errors.New("scale must be 1, 10, 100, or 1000")
	}
	switch s.Dimension {
	case "population", "history":
		if s.Relation != "" {
			return errors.New("only fanout accepts a relationship")
		}
	case "fanout":
		if s.Relation != "devices" && s.Relation != "messages" && s.Relation != "automations" {
			return errors.New("fanout relation must be devices, messages, or automations")
		}
	case "combined":
		if s.Scale != 10 || s.Relation != "" {
			return errors.New("combined is explicitly population10 x devices10 x history10")
		}
	default:
		return fmt.Errorf("unknown dimension %q", s.Dimension)
	}
	if s.Name == "" || filepath.Base(s.Name) != s.Name || s.Name == "." || s.Name == ".." {
		return errors.New("scenario name must be one path component")
	}
	return nil
}

func createOwnedDirectory(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("output directory must not exist: %s", path)
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, markerName), []byte("hearth-dbload/v1\n"), 0o600)
}

func requireOwnedDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("fixture directory must be an owned real directory")
	}
	data, err := os.ReadFile(filepath.Join(path, markerName))
	if err != nil || string(data) != "hearth-dbload/v1\n" {
		return errors.New("missing suite ownership marker")
	}
	return nil
}
