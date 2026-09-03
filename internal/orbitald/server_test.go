package orbitald

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeExecutor struct {
	ensureImageErr error
	ensuredImages  []string
}

func (f *fakeExecutor) EnsureImage(_ context.Context, imageRef string) error {
	f.ensuredImages = append(f.ensuredImages, imageRef)
	return f.ensureImageErr
}

func (f *fakeExecutor) Run(_ context.Context, _ FunctionSpec, _ WindowRecord) (ResultRecord, error) {
	return ResultRecord{}, nil
}

func TestHandleHealth(t *testing.T) {
	app := &App{store: &Store{}}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()

	app.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}

	var response struct {
		Status   string    `json:"status"`
		Version  string    `json:"version"`
		NodeTime time.Time `json:"node_time"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "ok" {
		t.Fatalf("expected health status ok, got %q", response.Status)
	}
	if response.Version != Version {
		t.Fatalf("expected version %q, got %q", Version, response.Version)
	}
	if response.NodeTime.IsZero() {
		t.Fatal("expected node_time to be set")
	}
}

func TestHandleSyncStoresFunctionsAndReturnsPendingResults(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "result.log")
	if err := os.WriteFile(logPath, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.state.Results = []ResultRecord{
		{
			ID:         "result-001",
			RunID:      "run-001",
			Function:   "capture",
			WindowID:   "pass-000",
			Area:       "zone-a",
			Status:     WindowSuccess,
			StartedAt:  now.Add(-time.Minute),
			FinishedAt: now.Add(-55 * time.Second),
			LogPath:    logPath,
		},
	}

	executor := &fakeExecutor{}
	app := &App{
		cfg:      Config{MaxLogBytes: 5},
		store:    store,
		executor: executor,
	}

	requestBody := SyncRequest{
		Functions: []FunctionSpec{{
			Name:  "capture",
			Image: "ghcr.io/example/capture",
		}},
		Windows: []WindowSpec{{
			ID:       "pass-001",
			Function: "capture",
			Area:     "zone-b",
			StartAt:  now.Add(time.Minute),
			EndAt:    now.Add(2 * time.Minute),
		}},
		ReplaceWindows: true,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/contact/sync", bytes.NewReader(body))
	recorder := httptest.NewRecorder()

	app.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if len(executor.ensuredImages) != 1 || executor.ensuredImages[0] != "ghcr.io/example/capture" {
		t.Fatalf("expected EnsureImage to be called for capture image, got %#v", executor.ensuredImages)
	}

	var response SyncResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Functions) != 1 || response.Functions[0].Name != "capture" {
		t.Fatalf("unexpected functions in response: %#v", response.Functions)
	}
	if len(response.Windows) != 1 || response.Windows[0].ID != "pass-001" {
		t.Fatalf("unexpected windows in response: %#v", response.Windows)
	}
	if len(response.PendingResults) != 1 {
		t.Fatalf("expected 1 pending result, got %d", len(response.PendingResults))
	}
	if response.PendingResults[0].Log != "hello" {
		t.Fatalf("expected truncated pending log %q, got %q", "hello", response.PendingResults[0].Log)
	}

	snapshot := store.Snapshot()
	if len(snapshot.Functions) != 1 || snapshot.Functions[0].Name != "capture" {
		t.Fatalf("unexpected stored functions: %#v", snapshot.Functions)
	}
	if len(snapshot.Windows) != 1 || snapshot.Windows[0].ID != "pass-001" {
		t.Fatalf("unexpected stored windows: %#v", snapshot.Windows)
	}
}

func TestHandleSyncRejectsImagePreparationError(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	app := &App{
		cfg:   Config{MaxLogBytes: DefaultMaxLogBytes},
		store: store,
		executor: &fakeExecutor{
			ensureImageErr: errors.New("pull failed"),
		},
	}

	now := time.Now().UTC()
	body, err := json.Marshal(SyncRequest{
		Functions: []FunctionSpec{{
			Name:  "capture",
			Image: "ghcr.io/example/capture",
		}},
		Windows: []WindowSpec{{
			ID:       "pass-001",
			Function: "capture",
			Area:     "zone-a",
			StartAt:  now,
			EndAt:    now.Add(time.Minute),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/contact/sync", bytes.NewReader(body))
	recorder := httptest.NewRecorder()

	app.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}
	if got := recorder.Body.String(); got != "pull failed\n" {
		t.Fatalf("expected error body %q, got %q", "pull failed\n", got)
	}
}
