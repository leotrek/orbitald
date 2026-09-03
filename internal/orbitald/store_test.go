package orbitald

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLifecycle(t *testing.T) {
	dir := t.TempDir()

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	function := FunctionSpec{
		Name:  "capture",
		Image: "ghcr.io/example/capture:latest",
	}
	if err := store.UpsertFunctions([]FunctionSpec{function}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	window := WindowSpec{
		ID:       "pass-001",
		Function: "capture",
		Area:     "zone-a",
		StartAt:  now.Add(-time.Minute),
		EndAt:    now.Add(time.Minute),
	}
	if err := store.UpsertWindows([]WindowSpec{window}, false); err != nil {
		t.Fatal(err)
	}

	claimed, ok, err := store.ClaimDueWindow(now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a runnable window")
	}
	if claimed.Status != WindowRunning {
		t.Fatalf("expected running window, got %s", claimed.Status)
	}

	result := ResultRecord{
		ID:         "result-001",
		RunID:      claimed.RunID,
		Function:   "capture",
		WindowID:   claimed.ID,
		Area:       claimed.Area,
		Status:     WindowSuccess,
		StartedAt:  now,
		FinishedAt: now.Add(10 * time.Second),
		LogPath:    dir + "/missing.log",
	}
	if err := store.CompleteWindow(claimed.ID, result); err != nil {
		t.Fatal(err)
	}

	pending, err := store.PendingResults(DefaultMaxLogBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending result, got %d", len(pending))
	}

	if err := store.AckResults([]string{"result-001"}); err != nil {
		t.Fatal(err)
	}

	pending, err = store.PendingResults(DefaultMaxLogBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending results after ack, got %d", len(pending))
	}
}

func TestStoreRecoversRunningWindow(t *testing.T) {
	dir := t.TempDir()

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.UpsertFunctions([]FunctionSpec{{
		Name:  "capture",
		Image: "ghcr.io/example/capture:latest",
	}}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := store.UpsertWindows([]WindowSpec{{
		ID:       "pass-002",
		Function: "capture",
		Area:     "zone-b",
		StartAt:  now.Add(-time.Minute),
		EndAt:    now.Add(time.Minute),
	}}, false); err != nil {
		t.Fatal(err)
	}

	claimed, ok, err := store.ClaimDueWindow(now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a runnable window")
	}

	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := reopened.Snapshot()
	if len(snapshot.Windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(snapshot.Windows))
	}
	if snapshot.Windows[0].Status != WindowPending {
		t.Fatalf("expected recovered window to return to pending, got %s", snapshot.Windows[0].Status)
	}
	if snapshot.Windows[0].RunID != "" {
		t.Fatalf("expected recovered window run id to be cleared, got %s", snapshot.Windows[0].RunID)
	}

	_ = claimed
}

func TestStoreRecoversExpiredWindows(t *testing.T) {
	dir := t.TempDir()

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	store.state.Functions["capture"] = FunctionSpec{
		Name:  "capture",
		Image: "ghcr.io/example/capture:latest",
	}
	store.state.Windows["running-expired"] = WindowRecord{
		WindowSpec: WindowSpec{
			ID:       "running-expired",
			Function: "capture",
			Area:     "zone-a",
			StartAt:  now.Add(-2 * time.Hour),
			EndAt:    now.Add(-time.Hour),
		},
		Status: WindowRunning,
		RunID:  "run-001",
	}
	store.state.Windows["pending-expired"] = WindowRecord{
		WindowSpec: WindowSpec{
			ID:       "pending-expired",
			Function: "capture",
			Area:     "zone-b",
			StartAt:  now.Add(-90 * time.Minute),
			EndAt:    now.Add(-30 * time.Minute),
		},
		Status: WindowPending,
	}
	if err := store.saveLocked(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := reopened.Snapshot()
	if len(snapshot.Windows) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(snapshot.Windows))
	}

	windowsByID := map[string]WindowRecord{}
	for _, window := range snapshot.Windows {
		windowsByID[window.ID] = window
	}

	runningExpired := windowsByID["running-expired"]
	if runningExpired.Status != WindowFailed {
		t.Fatalf("expected expired running window to fail, got %s", runningExpired.Status)
	}
	if runningExpired.Error != "orbitald restarted during execution" {
		t.Fatalf("unexpected running window recovery error %q", runningExpired.Error)
	}
	if runningExpired.FinishedAt == nil {
		t.Fatal("expected expired running window to have finished_at set")
	}

	pendingExpired := windowsByID["pending-expired"]
	if pendingExpired.Status != WindowExpired {
		t.Fatalf("expected expired pending window to expire, got %s", pendingExpired.Status)
	}
	if pendingExpired.Error != "window closed while orbitald was offline" {
		t.Fatalf("unexpected pending window recovery error %q", pendingExpired.Error)
	}
	if pendingExpired.FinishedAt == nil {
		t.Fatal("expected expired pending window to have finished_at set")
	}
}

func TestPendingResultsSortsAndTruncatesLogs(t *testing.T) {
	dir := t.TempDir()

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	olderLogPath := filepath.Join(dir, "older.log")
	if err := os.WriteFile(olderLogPath, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	newerLogPath := filepath.Join(dir, "newer.log")
	if err := os.WriteFile(newerLogPath, []byte("xyz"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	confirmedAt := now.Add(-time.Minute)
	store.state.Results = []ResultRecord{
		{
			ID:         "newer",
			RunID:      "run-002",
			Function:   "capture",
			WindowID:   "pass-002",
			Area:       "zone-b",
			Status:     WindowSuccess,
			StartedAt:  now,
			FinishedAt: now.Add(5 * time.Second),
			LogPath:    newerLogPath,
		},
		{
			ID:                "confirmed",
			RunID:             "run-003",
			Function:          "capture",
			WindowID:          "pass-003",
			Area:              "zone-c",
			Status:            WindowSuccess,
			StartedAt:         now.Add(-30 * time.Second),
			FinishedAt:        now.Add(-25 * time.Second),
			LogPath:           newerLogPath,
			UploadConfirmedAt: &confirmedAt,
		},
		{
			ID:         "older",
			RunID:      "run-001",
			Function:   "capture",
			WindowID:   "pass-001",
			Area:       "zone-a",
			Status:     WindowSuccess,
			StartedAt:  now.Add(-time.Minute),
			FinishedAt: now.Add(-55 * time.Second),
			LogPath:    olderLogPath,
		},
	}

	pending, err := store.PendingResults(4)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending results, got %d", len(pending))
	}

	if pending[0].ID != "older" || pending[1].ID != "newer" {
		t.Fatalf("expected results sorted by started_at, got %q then %q", pending[0].ID, pending[1].ID)
	}
	if pending[0].Log != "abcd" {
		t.Fatalf("expected truncated log %q, got %q", "abcd", pending[0].Log)
	}
	if pending[1].Log != "xyz" {
		t.Fatalf("expected full log %q, got %q", "xyz", pending[1].Log)
	}
}
