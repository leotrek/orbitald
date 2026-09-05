package orbitald

import (
	"testing"
	"time"
)

func TestBuildTaskInfosReturnsAllSnapshotStates(t *testing.T) {
	start := mustTime(t, "2026-09-05T12:00:00Z")
	finish := mustTime(t, "2026-09-05T12:01:00Z")

	tasks := BuildTaskInfos(StateSnapshot{
		Functions: []FunctionSpec{
			{Name: "capture", Image: "ghcr.io/acme/capture:latest"},
		},
		Windows: []WindowRecord{
			{
				WindowSpec: WindowSpec{ID: "window-pending", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:     WindowPending,
			},
			{
				WindowSpec:  WindowSpec{ID: "window-running", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:      WindowRunning,
				RunID:       "run-running",
				TriggeredAt: &start,
			},
			{
				WindowSpec:  WindowSpec{ID: "window-ok", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:      WindowSuccess,
				RunID:       "run-ok",
				TriggeredAt: &start,
				FinishedAt:  &finish,
			},
			{
				WindowSpec:  WindowSpec{ID: "window-failed", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:      WindowFailed,
				RunID:       "run-failed",
				TriggeredAt: &start,
				FinishedAt:  &finish,
				Error:       "window failed",
			},
			{
				WindowSpec: WindowSpec{ID: "window-expired", Function: "capture", Area: "manual", StartAt: start, EndAt: start.Add(time.Minute)},
				Status:     WindowExpired,
				FinishedAt: &finish,
				Error:      "window closed",
			},
		},
		Results: []ResultRecord{
			{ID: "result-ok", RunID: "run-ok", Function: "capture", WindowID: "window-ok", Status: WindowSuccess, ExitCode: 0, StartedAt: start, FinishedAt: finish},
			{ID: "result-failed", RunID: "run-failed", Function: "capture", WindowID: "window-failed", Status: WindowFailed, ExitCode: 1, StartedAt: start, FinishedAt: finish, Error: "boom"},
		},
	}, "")

	statuses := map[string]string{}
	for _, task := range tasks {
		statuses[task.ID] = task.Status
	}

	assertStatus(t, statuses, "window-pending", "pending")
	assertStatus(t, statuses, "run-running", "running")
	assertStatus(t, statuses, "run-ok", "stopped")
	assertStatus(t, statuses, "run-failed", "error")
	assertStatus(t, statuses, "window-expired", "expired")
}

func TestBuildTaskInfosIncludesOrphanResults(t *testing.T) {
	start := mustTime(t, "2026-09-05T12:00:00Z")
	finish := mustTime(t, "2026-09-05T12:01:00Z")

	tasks := BuildTaskInfos(StateSnapshot{
		Results: []ResultRecord{
			{ID: "result-orphan", RunID: "run-orphan", Function: "capture", WindowID: "missing-window", Status: WindowFailed, ExitCode: 2, StartedAt: start, FinishedAt: finish, Error: "lost window"},
		},
	}, "")

	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "run-orphan" || tasks[0].Status != "error" || tasks[0].Error != "lost window" {
		t.Fatalf("unexpected orphan task: %#v", tasks[0])
	}
}

func TestMergeContainerInfosIncludesOrphanContainers(t *testing.T) {
	start := mustTime(t, "2026-09-05T12:00:00Z")
	tasks := []TaskInfo{
		{ID: "run-existing", RunID: "run-existing", Function: "capture", Status: "running"},
	}
	containers := []ContainerInfo{
		{ID: "run-existing", Image: "ghcr.io/acme/capture:latest", TaskStatus: "running", CreatedAt: start},
		{ID: "orphan-container", Image: "ghcr.io/acme/orphan:latest", TaskStatus: "running", CreatedAt: start.Add(time.Minute)},
	}

	merged := MergeContainerInfos(tasks, containers, "")
	byID := map[string]TaskInfo{}
	for _, item := range merged {
		byID[item.ID] = item
	}

	existing := byID["run-existing"]
	if existing.ContainerID != "run-existing" || existing.Image != "ghcr.io/acme/capture:latest" {
		t.Fatalf("existing task did not receive container data: %#v", existing)
	}

	orphan := byID["orphan-container"]
	if orphan.ContainerID != "orphan-container" || orphan.Status != "running" || orphan.Image != "ghcr.io/acme/orphan:latest" {
		t.Fatalf("orphan container was not included as a task: %#v", orphan)
	}
}

func TestTailLogReturnsLastLines(t *testing.T) {
	got := string(TailLog([]byte("one\ntwo\nthree\n"), 2))
	if got != "two\nthree\n" {
		t.Fatalf("unexpected tail %q", got)
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
