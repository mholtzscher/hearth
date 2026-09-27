package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

func TestLoadYAML(t *testing.T) {
	t.Parallel()
	type value struct {
		Name string `yaml:"name"`
	}
	for _, tc := range []struct {
		name, body, want, err string
		explicit              bool
		missing               bool
	}{
		{name: "missing implicit", missing: true},
		{name: "missing explicit", missing: true, explicit: true, err: "configuration file could not be read"},
		{name: "empty", explicit: true},
		{name: "unknown keys", body: "unknown: ignored\nname: ok\n", explicit: true, want: "ok"},
		{name: "invalid", body: "name: [secret-value\n", explicit: true, err: "configuration file contains invalid YAML"},
		{name: "multiple", body: "name: ok\n---\nname: next\n", explicit: true, err: "configuration file must contain a single YAML document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "sensitive-path.yaml")
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				_ = os.Remove(path)
			}
			got, err := platformconfig.LoadYAML[value](path, tc.explicit)
			assertLoadError(t, err, tc.err)
			if err != nil && (strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "secret-value")) {
				t.Fatalf("error leaked path or value: %v", err)
			}
			if err == nil && got.Name != tc.want {
				t.Fatalf("name = %q, want %q", got.Name, tc.want)
			}
		})
	}
}

func assertLoadError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" && err != nil || want != "" && (err == nil || err.Error() != want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
