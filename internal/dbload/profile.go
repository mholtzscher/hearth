package dbload

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

type queryPlan struct {
	Query   string   `json:"query"`
	SQL     string   `json:"sql"`
	Args    []any    `json:"args"`
	Details []string `json:"details"`
	Gap     string   `json:"gap,omitempty"`
}

func capturePlans(ctx context.Context, database *sql.DB, sqlText map[string]string, w Workload, path string) error {
	var plans []queryPlan
	for _, name := range w.Queries {
		p := queryPlan{Query: name, SQL: sqlText[name]}
		args, ok := w.PlanArgs[name]
		if !ok || p.SQL == "" {
			p.Gap = "representative bind parameters or exact SQL not captured for this statement"
			plans = append(plans, p)
			continue
		}
		p.Args = args
		rows, err := database.QueryContext(ctx, "EXPLAIN QUERY PLAN "+p.SQL, args...)
		if err != nil {
			p.Gap = err.Error()
			plans = append(plans, p)
			continue
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				break
			}
			p.Details = append(p.Details, detail)
		}
		if err == nil {
			err = rows.Err()
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			p.Gap = err.Error()
		}
		plans = append(plans, p)
	}
	return WriteJSON(path, plans)
}

func environment() map[string]string {
	result := map[string]string{
		"go":                runtime.Version(),
		"os":                runtime.GOOS,
		"arch":              runtime.GOARCH,
		"logical_cpus":      strconv.Itoa(runtime.NumCPU()),
		"connection_policy": "production db.Open: WAL, foreign_keys=1, busy_timeout=5000, max_open_connections=1",
		"cache":             "warm; no host cache flush",
		"load_model":        "closed-loop; not maximum sustainable arrival rate",
	}
	for _, path := range []string{"/proc/loadavg", "/proc/meminfo", "/proc/self/status"} {
		if data, err := os.ReadFile(path); err == nil {
			result[path] = string(data)
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "modernc.org/sqlite" {
				result[dep.Path] = dep.Version
			}
		}
		for _, setting := range info.Settings {
			if strings.HasPrefix(setting.Key, "vcs.") {
				result[setting.Key] = setting.Value
			}
		}
	}
	return result
}

// HostLoad reports the one-minute scheduler load without changing any host setting.
func HostLoad() (float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty load average")
	}
	return strconv.ParseFloat(fields[0], 64)
}
