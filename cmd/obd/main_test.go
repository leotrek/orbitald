package main

import (
	"testing"
	"time"

	"github.com/orbitald/orbitald/internal/orbitald"
)

func TestSortedFunctionsReturnsAllFunctionsByName(t *testing.T) {
	functions := sortedFunctions([]orbitald.FunctionSpec{
		{Name: "capture", Image: "ghcr.io/acme/capture:latest"},
		{Name: "analyze", Image: "ghcr.io/acme/analyze:latest"},
	})

	if len(functions) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(functions))
	}
	if functions[0].Name != "analyze" || functions[1].Name != "capture" {
		t.Fatalf("functions were not sorted by name: %#v", functions)
	}
}

func TestBuildInstanceInfosReturnsAllSnapshotStates(t *testing.T) {
	start := mustTime(t, "2026-09-05T12:00:00Z")
	finish := mustTime(t, "2026-09-05T12:01:00Z")

	instances := buildInstanceInfos(orbitald.StateSnapshot{
		Functions: []orbitald.FunctionSpec{
			{Name: "capture", Image: "ghcr.io/acme/capture:latest"},
		},
		Windows: []orbitald.WindowRecord{
			{
				WindowSpec: orbitald.WindowSpec{ID: "window-pending", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:     orbitald.WindowPending,
			},
			{
				WindowSpec:  orbitald.WindowSpec{ID: "window-running", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:      orbitald.WindowRunning,
				RunID:       "run-running",
				TriggeredAt: &start,
			},
			{
				WindowSpec:  orbitald.WindowSpec{ID: "window-ok", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:      orbitald.WindowSuccess,
				RunID:       "run-ok",
				TriggeredAt: &start,
				FinishedAt:  &finish,
			},
			{
				WindowSpec:  orbitald.WindowSpec{ID: "window-failed", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:      orbitald.WindowFailed,
				RunID:       "run-failed",
				TriggeredAt: &start,
				FinishedAt:  &finish,
				Error:       "window failed",
			},
			{
				WindowSpec: orbitald.WindowSpec{ID: "window-expired", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:     orbitald.WindowExpired,
				FinishedAt: &finish,
				Error:      "window closed",
			},
		},
		Results: []orbitald.ResultRecord{
			{ID: "result-ok", RunID: "run-ok", Function: "capture", WindowID: "window-ok", Status: orbitald.WindowSuccess, ExitCode: 0, StartedAt: start, FinishedAt: finish},
			{ID: "result-failed", RunID: "run-failed", Function: "capture", WindowID: "window-failed", Status: orbitald.WindowFailed, ExitCode: 1, StartedAt: start, FinishedAt: finish, Error: "boom"},
		},
	}, "")

	statuses := map[string]string{}
	for _, instance := range instances {
		statuses[instance.ID] = instance.Status
	}

	assertStatus(t, statuses, "window-pending", "pending")
	assertStatus(t, statuses, "run-running", "running")
	assertStatus(t, statuses, "run-ok", "stopped")
	assertStatus(t, statuses, "run-failed", "error")
	assertStatus(t, statuses, "window-expired", "expired")
}

func TestBuildInstanceInfosIncludesOrphanResults(t *testing.T) {
	start := mustTime(t, "2026-09-05T12:00:00Z")
	finish := mustTime(t, "2026-09-05T12:01:00Z")

	instances := buildInstanceInfos(orbitald.StateSnapshot{
		Results: []orbitald.ResultRecord{
			{ID: "result-orphan", RunID: "run-orphan", Function: "capture", WindowID: "missing-window", Status: orbitald.WindowFailed, ExitCode: 2, StartedAt: start, FinishedAt: finish, Error: "lost window"},
		},
	}, "")

	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	if instances[0].ID != "run-orphan" || instances[0].Status != "error" || instances[0].Error != "lost window" {
		t.Fatalf("unexpected orphan instance: %#v", instances[0])
	}
}

func assertStatus(t *testing.T, statuses map[string]string, id, want string) {
	t.Helper()
	if got := statuses[id]; got != want {
		t.Fatalf("expected %s to be %q, got %q", id, want, got)
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
