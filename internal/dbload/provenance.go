package dbload

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func readVersionedSource(root, revision, path string) ([]byte, error) {
	if revision == "" {
		return os.ReadFile(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	//nolint:gosec // Fixed git operation over a build revision and suite-owned source paths.
	command := exec.CommandContext(context.Background(), "git", "show", revision+":"+filepath.ToSlash(rel))
	command.Dir = root
	return command.Output()
}

type binaryIdentity struct {
	Revision      string            `json:"revision"`
	Go            string            `json:"go"`
	Compatibility map[string]string `json:"compatibility"`
}

func identity(path string) (binaryIdentity, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return binaryIdentity{}, err
	}
	result := binaryIdentity{
		Go:            info.GoVersion,
		Compatibility: map[string]string{"go": info.GoVersion, "path": info.Path},
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			result.Revision = setting.Value
		}
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			return result, errors.New("confirmation binaries must be built from clean committed sources")
		}
		if !strings.HasPrefix(setting.Key, "vcs.") {
			result.Compatibility[setting.Key] = setting.Value
		}
	}
	for _, dep := range info.Deps {
		result.Compatibility["dependency:"+dep.Path] = dep.Version + " " + dep.Sum
		if dep.Replace != nil {
			return result, errors.New("replacement dependencies require an explicit comparable-build protocol")
		}
	}
	if result.Revision == "" {
		return result, errors.New("confirmation binaries require recorded VCS revisions")
	}
	return result, nil
}

func writeExecutionSnapshot(cfg Config, baseline, candidate string) error {
	b, err := identity(baseline)
	if err != nil {
		return err
	}
	c, err := identity(candidate)
	if err != nil {
		return err
	}
	bjson, err := json.Marshal(b.Compatibility)
	if err != nil {
		return err
	}
	cjson, err := json.Marshal(c.Compatibility)
	if err != nil {
		return err
	}
	if string(bjson) != string(cjson) {
		return errors.New("baseline and candidate toolchain, flags, or dependency graph differ")
	}
	return WriteJSON(
		filepath.Join(cfg.OutputDir, "execution.json"),
		map[string]any{
			"baseline":           b,
			"candidate":          c,
			"cases":              cfg.Cases,
			"concurrency":        cfg.Concurrency,
			"operation_limit_ns": cfg.OperationLimit.Nanoseconds(),
			"case_limit_ns":      cfg.CaseLimit.Nanoseconds(),
			"budget_ns":          cfg.Budget.Nanoseconds(),
			"disk_limit_bytes":   cfg.MaxDiskBytes,
			"rss_limit_bytes":    cfg.MaxWorkerRSS,
			"cache":              "equal untimed warmup; warm OS cache; no host cache flush",
		},
	)
}

func prepareConfirmationImages(ctx context.Context, cfg *Config, baseline, candidate string, data *ReportData) error {
	for _, side := range []struct{ variant, binary string }{{variantBaseline, baseline}, {variantCandidate, candidate}} {
		path := filepath.Join(cfg.OutputDir, "prepared-"+side.variant)
		args := []string{
			"prepare",
			"-fixture",
			cfg.Fixture,
			"-out",
			path,
			"-source-root",
			cfg.SourceRoot,
			"-budget",
			cfg.Budget.String(),
			"-resource-root",
			cfg.ResourceRoot,
		}
		if side.variant == variantBaseline {
			args = append(args, "-strict-baseline")
		}
		//nolint:gosec // Registered immutable experiment binaries, already checked for build compatibility.
		command := exec.CommandContext(ctx, side.binary, args...)
		command.Env = append(os.Environ(), "GOMEMLIMIT=4GiB")
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("prepare %s: %w: %s", side.variant, err, output)
		}
		manifest, err := ReadManifest(filepath.Join(path, fixtureManifestName))
		if err != nil {
			return err
		}
		data.Manifests = append(data.Manifests, manifest)
		data.Artifacts = append(data.Artifacts, filepath.Join(path, fixtureManifestName))
	}
	return nil
}
