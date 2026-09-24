// Command validation-stack prepares and checks the real-device validation stack.
// Mise/Pitchfork owns its long-running processes; the simulator needs no helper.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"syscall"
	"time"
)

type stackRecord struct {
	Mode              string `json:"mode"`
	Host              string `json:"host,omitempty"`
	Worktree          string `json:"worktree"`
	PreviousRuntimeID string `json:"previous_runtime_id,omitempty"`
}

type daemonInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type stack struct {
	root  string
	ports map[string]int
}

const (
	realMode       = "real"
	realWeb        = "real-web"
	remoteCorePort = 8081
	httpTimeout    = 2 * time.Second
)

func main() {
	if err := runValidationStack(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "validation-stack:", err)
		os.Exit(1)
	}
}

//nolint:mnd // CLI dispatch keeps flags close to the action they govern.
func runValidationStack(args []string) error {
	if len(args) == 0 {
		return errors.New("expected a validation stack lifecycle or readiness action")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	s := stack{root: root}
	switch args[0] {
	case "ready-real-adapter":
		return s.readyRealAdapter()
	case "ready-dashboard":
		if len(args) != 2 {
			return errors.New("ready-dashboard requires real-web")
		}
		return s.readyDashboard(args[1])
	case "start-real", "stop-real":
		return s.withLifecycleLock(func() error {
			switch args[0] {
			case "start-real":
				if len(args) != 2 {
					return errors.New("start-real requires a homelab hostname")
				}
				return s.startReal(args[1])
			default:
				return s.stop()
			}
		})
	default:
		return fmt.Errorf("unknown validation stack action %q", args[0])
	}
}

func (s *stack) path(name string) string {
	return filepath.Join(s.root, ".data", "validation-stack", name)
}
func (s *stack) recordPath() string { return filepath.Join(s.root, ".data", "validation-stack.json") }

//nolint:govet // Lock acquisition and release errors refer to distinct operations.
func (s *stack) withLifecycleLock(action func() error) error {
	if err := os.MkdirAll(filepath.Join(s.root, ".data"), 0o750); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.root, ".data", "validation-stack.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("validation stack lifecycle operation already active: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return action()
}

func (s *stack) command(output bool, argv ...string) ([]byte, error) {
	//nolint:gosec // Argument vectors are selected by this CLI; no shell interpretation.
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Dir = s.root
	if output {
		return cmd.CombinedOutput()
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return nil, cmd.Run()
}

//nolint:govet // Decode and per-port conversion errors are checked independently.
func (s *stack) loadPorts() error {
	data, err := s.command(true, "mise", "env", "--json")
	if err != nil {
		return fmt.Errorf("mise port exports: %w: %s", err, data)
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	s.ports = make(map[string]int)
	for _, name := range []string{"REAL_CORE_PORT", "REAL_WEB_PORT"} {
		port, err := strconv.Atoi(values[name])
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("mise did not export a valid %s", name)
		}
		s.ports[name] = port
	}
	return nil
}

//nolint:govet // Version parsing has a separate error from command execution.
func (s *stack) checkMiseVersion() error {
	data, err := s.command(true, "mise", "--version")
	if err != nil {
		return err
	}
	var year, month, patch int
	if _, err := fmt.Sscanf(string(data), "%d.%d.%d", &year, &month, &patch); err != nil {
		return fmt.Errorf("cannot parse mise version: %w", err)
	}
	if year < 2026 || (year == 2026 && (month < 9 || (month == 9 && patch < 12))) {
		return errors.New("mise 2026.9.12 or newer is required for worktree ports")
	}
	return nil
}

func (s *stack) daemons() ([]daemonInfo, error) {
	data, err := s.command(true, "mise", "daemons", "ls", "--json")
	if err != nil {
		return nil, fmt.Errorf("list validation daemons: %w: %s", err, data)
	}
	var result []daemonInfo
	return result, json.Unmarshal(data, &result)
}

func (s *stack) requireAbsent() error {
	if _, err := os.Stat(s.recordPath()); err == nil {
		return errors.New("validation stack record exists; stop the recorded stack first")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	daemons, err := s.daemons()
	if err != nil {
		return err
	}
	for _, daemon := range daemons {
		if daemon.Status == "running" || daemon.Status == "starting" {
			return fmt.Errorf("validation stack daemon %s is already active; inspect before starting", daemon.Name)
		}
	}
	return nil
}

func (s *stack) save(record stackRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return os.WriteFile(s.recordPath(), append(data, '\n'), 0o600)
}

//nolint:govet // Parse and read failures refer to distinct steps.
func (s *stack) record(mode string) (stackRecord, error) {
	data, err := os.ReadFile(s.recordPath())
	if err != nil {
		return stackRecord{}, fmt.Errorf("validation stack record missing; start %s first: %w", mode, err)
	}
	var record stackRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return record, err
	}
	if record.Worktree != s.root || record.Mode != mode {
		return record, fmt.Errorf("validation stack record belongs to %s/%s, not %s/%s",
			record.Worktree, record.Mode, s.root, mode)
	}
	return record, nil
}

func (s *stack) writeConfig(name string, data []byte) error {
	if err := os.MkdirAll(s.path(""), 0o750); err != nil {
		return err
	}
	//nolint:gosec // Callers select fixed names for files under the private worktree data directory.
	return os.WriteFile(s.path(name), data, 0o600)
}

func (s *stack) agentConfig() string {
	home, _ := os.UserHomeDir()
	return fmt.Sprintf("agent:\n  api_key_file: %s\n  reasoning_effort: none\n",
		filepath.Join(home, ".local/share/agenix/hearth-openai-api-key"))
}

func (s *stack) installWeb() error {
	_, err := os.Stat(filepath.Join(s.root, "web", "node_modules"))
	if errors.Is(err, os.ErrNotExist) {
		_, err = s.command(false, "mise", "run", "web-install")
		return err
	}
	return err
}

//nolint:govet,gocognit // Remote checks must finish before any local process starts.
func (s *stack) startReal(host string) error {
	if err := s.checkMiseVersion(); err != nil {
		return err
	}
	if err := s.requireAbsent(); err != nil {
		return err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`).MatchString(host) {
		return errors.New("real-device host must be a hostname or IPv4 address, not a URL")
	}
	if portOpen(host, remoteCorePort) {
		return errors.New("remote core :8081 is active; ask the operator before proceeding")
	}
	for _, url := range []string{"http://" + net.JoinHostPort(host, "8222") + "/varz",
		"http://" + net.JoinHostPort(host, "8082") + "/"} {
		if err := checkHTTP(url); err != nil {
			return fmt.Errorf("real-device preflight %s: %w", url, err)
		}
	}
	for _, port := range []int{4222, 1883} {
		if !portOpen(host, port) {
			return fmt.Errorf("real-device preflight %s:%d is unreachable", host, port)
		}
	}
	if err := s.loadPorts(); err != nil {
		return err
	}
	store := s.path("storage")
	if err := os.MkdirAll(store, 0o750); err != nil {
		return err
	}
	hearthd := fmt.Sprintf("http_addr: 127.0.0.1:%d\nnats_url: nats://%s:4222\n"+
		"sqlite_path: %s\nhousehold_timezone: UTC\n%s",
		s.ports["REAL_CORE_PORT"], host, filepath.Join(store, "real-hearthd.db"), s.agentConfig())
	adapter := fmt.Sprintf("adapter_id: zigbee2mqtt\nnats_url: nats://%s:4222\n"+
		"mqtt:\n  url: tcp://%s:1883\n  base_topic: zigbee2mqtt\n", host, host)
	if err := s.writeConfig("real-hearthd.yaml", []byte(hearthd)); err != nil {
		return err
	}
	if err := s.writeConfig("real-zigbee2mqtt.yaml", []byte(adapter)); err != nil {
		return err
	}
	// The hostname is syntax-checked before writing a shell-sourced environment file.
	monitorURL := "NATS_MONITOR_URL=http://" + net.JoinHostPort(host, "8222") + "\n"
	if err := s.writeConfig("real-web.env", []byte(monitorURL)); err != nil {
		return err
	}
	if err := s.installWeb(); err != nil {
		return err
	}
	if err := s.save(stackRecord{Mode: realMode, Host: host, Worktree: s.root}); err != nil {
		return err
	}
	if _, err := s.command(false, "mise", "daemons", "start", "real-core"); err != nil {
		return fmt.Errorf("start real-device Core (cleanup: mise run real-device-stop): %w", err)
	}
	core := loopbackURL(s.ports["REAL_CORE_PORT"])
	previous, err := readAdapter(core, "zigbee2mqtt")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record := stackRecord{Mode: realMode, Host: host, Worktree: s.root, PreviousRuntimeID: previous}
	if err := s.save(record); err != nil {
		return err
	}
	if _, err := s.command(false, "mise", "daemons", "start", "real-adapter", "real-web"); err != nil {
		return fmt.Errorf("start real-device adapter (cleanup: mise run real-device-stop): %w", err)
	}
	fmt.Fprintf(os.Stdout, "Ready for validation: Core %s; dashboard %s/\n",
		core, loopbackURL(s.ports["REAL_WEB_PORT"]))
	fmt.Fprintln(os.Stdout, "No tailnet route published. Shared homelab remains operator-managed.")
	return nil
}

//nolint:govet // Stop and record removal errors are checked independently.
func (s *stack) stop() error {
	if _, err := s.record(realMode); err != nil {
		return err
	}
	daemons, err := s.daemons()
	if err != nil {
		return err
	}
	names := []string{realWeb, "real-adapter", "real-core"}
	if hasActiveStackDaemon(names, daemons) {
		if _, err := s.command(false, append([]string{"mise", "daemons", "stop"}, names...)...); err != nil {
			return err
		}
	}
	if err := os.Remove(s.recordPath()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Stopped real-device daemons; evidence remains in %s\n", s.path(""))
	return nil
}

func hasActiveStackDaemon(names []string, daemons []daemonInfo) bool {
	for _, daemon := range daemons {
		if (daemon.Status == "running" || daemon.Status == "starting") && slices.Contains(names, daemon.Name) {
			return true
		}
	}
	return false
}

func loopbackURL(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) }

//nolint:gosec // Targets are loopback or the explicitly selected homelab host.
func checkHTTP(url string) error {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: httpTimeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s", response.StatusCode, url)
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}

func checkDashboard(url string) error {
	for _, path := range []string{"/", "/readyz", "/nats-monitor/varz"} {
		if err := checkHTTP(url + path); err != nil {
			return err
		}
	}
	return nil
}

func portOpen(host string, port int) bool {
	conn, err := (&net.Dialer{Timeout: httpTimeout}).DialContext(context.Background(), "tcp",
		net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

type adapterBody struct {
	Health struct {
		Status  string `json:"status"`
		Runtime struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"runtime"`
	} `json:"health"`
}

func readAdapterBody(core, id string) (adapterBody, error) {
	var body adapterBody
	url := core + "/v1/adapters/" + id
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return body, err
	}
	response, err := (&http.Client{Timeout: httpTimeout}).Do(request)
	if err != nil {
		return body, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return body, os.ErrNotExist
	}
	if response.StatusCode != http.StatusOK {
		return body, fmt.Errorf("adapter health %s: HTTP %d", url, response.StatusCode)
	}
	return body, json.NewDecoder(response.Body).Decode(&body)
}

func readAdapter(core, id string) (string, error) {
	body, err := readAdapterBody(core, id)
	return body.Health.Runtime.ID, err
}

func checkAdapter(core, id, previousID string) error {
	body, err := readAdapterBody(core, id)
	if err != nil {
		return err
	}
	if body.Health.Runtime.ID == "" || body.Health.Runtime.ID == previousID || body.Health.Runtime.Status != "online" {
		return errors.New("adapter has no fresh online runtime")
	}
	if id == "zigbee2mqtt" && body.Health.Status != "healthy" {
		return errors.New("real-device adapter is not healthy")
	}
	return nil
}

func (s *stack) readyRealAdapter() error {
	if err := s.loadPorts(); err != nil {
		return err
	}
	record, err := s.record(realMode)
	if err != nil {
		return err
	}
	return checkAdapter(loopbackURL(s.ports["REAL_CORE_PORT"]), "zigbee2mqtt", record.PreviousRuntimeID)
}

func (s *stack) readyDashboard(service string) error {
	if service != realWeb {
		return fmt.Errorf("unknown dashboard service %q", service)
	}
	if _, err := s.record(realMode); err != nil {
		return err
	}
	if err := s.loadPorts(); err != nil {
		return err
	}
	return checkDashboard(loopbackURL(s.ports["REAL_WEB_PORT"]))
}
