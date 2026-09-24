package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAdapterRejectsStaleRuntime(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/adapters/zigbee2mqtt" {
			t.Errorf("unexpected path %s", request.URL.Path)
		}
		json.NewEncoder(writer).Encode(map[string]any{"health": map[string]any{
			"status": "healthy", "runtime": map[string]string{"id": "old", "status": "online"},
		}})
	}))
	defer server.Close()
	staleErr := checkAdapter(server.URL, "zigbee2mqtt", "old")
	if staleErr == nil || !strings.Contains(staleErr.Error(), "fresh online runtime") {
		t.Fatalf("stale persisted runtime was accepted: %v", staleErr)
	}
	if err := checkAdapter(server.URL, "zigbee2mqtt", "earlier"); err != nil {
		t.Fatalf("new healthy runtime was rejected: %v", err)
	}
}

func TestRecordRejectsAnotherWorktreeOrMode(t *testing.T) {
	t.Parallel()
	s := stack{root: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(s.recordPath()), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := s.save(stackRecord{Worktree: "/elsewhere", Mode: "simulator"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.record("simulator"); err == nil {
		t.Fatal("another worktree's record was accepted")
	}
	if err := s.save(stackRecord{Worktree: s.root, Mode: "real"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.record("simulator"); err == nil {
		t.Fatal("real-device record was accepted as simulator ownership")
	}
}

func TestCheckDashboardRequiresMonitorProxy(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/nats-monitor/varz" {
			http.Error(writer, "monitor proxy unavailable", http.StatusBadGateway)
		}
	}))
	defer server.Close()
	if err := checkDashboard(server.URL); err == nil || !strings.Contains(err.Error(), "/nats-monitor/varz") {
		t.Fatalf("dashboard accepted a broken monitoring proxy: %v", err)
	}
}

func TestReadyDashboardRejectsWrongModeBeforeProbing(t *testing.T) {
	t.Parallel()
	s := stack{root: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(s.recordPath()), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := s.save(stackRecord{Worktree: s.root, Mode: "simulator"}); err != nil {
		t.Fatal(err)
	}
	if err := s.readyDashboard("real-web"); err == nil {
		t.Fatal("real dashboard readiness accepted a simulator ownership record")
	}
}
