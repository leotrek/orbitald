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
	"strings"
	"testing"
	"time"
)

type fakeExecutor struct {
	ensureImageErr error
	ensuredImages  []string
	containers     []ContainerInfo
	listErr        error
	stopped        []string
	stopErr        error
}

func (f *fakeExecutor) EnsureImage(_ context.Context, imageRef string) error {
	f.ensuredImages = append(f.ensuredImages, imageRef)
	return f.ensureImageErr
}

func (f *fakeExecutor) Run(_ context.Context, _ FunctionSpec, _ WindowRecord) (ResultRecord, error) {
	return ResultRecord{}, nil
}

func (f *fakeExecutor) ListContainers(_ context.Context) ([]ContainerInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]ContainerInfo(nil), f.containers...), nil
}

func (f *fakeExecutor) ContainerInfo(_ context.Context, id string) (ContainerInfo, error) {
	for _, container := range f.containers {
		if container.ID == id {
			return container, nil
		}
	}
	return ContainerInfo{}, errors.New("container not found")
}

func (f *fakeExecutor) StopContainerTask(_ context.Context, id string) error {
	f.stopped = append(f.stopped, id)
	return f.stopErr
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

func TestHandleImagesAndImageDetails(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	store.state.Functions["capture"] = FunctionSpec{Name: "capture", Image: "ghcr.io/acme/capture:latest"}
	store.state.Functions["analyze"] = FunctionSpec{Name: "analyze", Image: "ghcr.io/acme/analyze:latest"}
	store.state.Windows["window-001"] = WindowRecord{
		WindowSpec: WindowSpec{ID: "window-001", Function: "capture", Area: "manual", StartAt: now, EndAt: now.Add(time.Minute)},
		Status:     WindowPending,
	}

	app := &App{store: store, executor: &fakeExecutor{}}

	listRecorder := httptest.NewRecorder()
	app.routes().ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/v1/images", nil))

	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, listRecorder.Code, listRecorder.Body.String())
	}
	var functions []FunctionSpec
	if err := json.NewDecoder(listRecorder.Body).Decode(&functions); err != nil {
		t.Fatal(err)
	}
	if len(functions) != 2 || functions[0].Name != "analyze" || functions[1].Name != "capture" {
		t.Fatalf("unexpected image list: %#v", functions)
	}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/images/capture", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	var response FunctionDetails
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Function.Name != "capture" || len(response.Windows) != 1 {
		t.Fatalf("unexpected image details: %#v", response)
	}
}

func TestHandleTaskListFiltersByFunction(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	store.state.Functions["capture"] = FunctionSpec{Name: "capture", Image: "ghcr.io/acme/capture:latest"}
	store.state.Functions["analyze"] = FunctionSpec{Name: "analyze", Image: "ghcr.io/acme/analyze:latest"}
	store.state.Windows["capture-window"] = WindowRecord{
		WindowSpec: WindowSpec{ID: "capture-window", Function: "capture", Area: "manual", StartAt: now, EndAt: now.Add(time.Minute)},
		Status:     WindowPending,
	}
	store.state.Windows["analyze-window"] = WindowRecord{
		WindowSpec: WindowSpec{ID: "analyze-window", Function: "analyze", Area: "manual", StartAt: now, EndAt: now.Add(time.Minute)},
		Status:     WindowPending,
	}
	executor := &fakeExecutor{
		containers: []ContainerInfo{
			{ID: "orphan-container", Image: "ghcr.io/acme/orphan:latest", TaskStatus: "running", CreatedAt: now},
		},
	}

	app := &App{store: store, executor: executor}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/tasks?function=capture", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	var response TaskListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tasks) != 1 {
		t.Fatalf("expected 1 filtered task, got %#v", response.Tasks)
	}
	if response.Tasks[0].Function != "capture" || response.Tasks[0].ID != "capture-window" {
		t.Fatalf("unexpected filtered task: %#v", response.Tasks[0])
	}
}

func TestHandleTaskListMergesRuntimeContainers(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	store.state.Functions["capture"] = FunctionSpec{Name: "capture", Image: "ghcr.io/acme/capture:latest"}
	store.state.Windows["window-running"] = WindowRecord{
		WindowSpec:  WindowSpec{ID: "window-running", Function: "capture", Area: "manual", StartAt: now, EndAt: now.Add(time.Minute)},
		Status:      WindowRunning,
		RunID:       "run-running",
		TriggeredAt: &now,
	}
	executor := &fakeExecutor{
		containers: []ContainerInfo{
			{ID: "run-running", Image: "ghcr.io/acme/capture:latest", TaskStatus: "running", CreatedAt: now},
			{ID: "orphan-container", Image: "ghcr.io/acme/orphan:latest", TaskStatus: "running", CreatedAt: now.Add(time.Second)},
		},
	}

	app := &App{store: store, executor: executor}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/tasks", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	var response TaskListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %#v", response.Tasks)
	}
	byID := map[string]TaskInfo{}
	for _, task := range response.Tasks {
		byID[task.ID] = task
	}
	if byID["run-running"].ContainerID != "run-running" {
		t.Fatalf("missing live container on task: %#v", byID["run-running"])
	}
	if byID["orphan-container"].Status != "running" {
		t.Fatalf("missing orphan container task: %#v", byID["orphan-container"])
	}
}

func TestHandleTaskInspectFindsResultAndLiveContainer(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	store.state.Functions["capture"] = FunctionSpec{Name: "capture", Image: "ghcr.io/acme/capture:latest"}
	store.state.Results = []ResultRecord{
		{ID: "result-ok", RunID: "run-ok", Function: "capture", WindowID: "window-ok", Status: WindowSuccess, ExitCode: 0, StartedAt: now, FinishedAt: now},
	}
	executor := &fakeExecutor{
		containers: []ContainerInfo{
			{ID: "run-ok", Image: "ghcr.io/acme/capture:latest", TaskStatus: "stopped", CreatedAt: now},
			{ID: "live-only", Image: "ghcr.io/acme/live:latest", TaskStatus: "running", CreatedAt: now},
		},
	}
	app := &App{store: store, executor: executor}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/tasks/result-ok", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	var fromResult TaskInfo
	if err := json.NewDecoder(recorder.Body).Decode(&fromResult); err != nil {
		t.Fatal(err)
	}
	if fromResult.ID != "run-ok" || fromResult.ResultID != "result-ok" || fromResult.ContainerID != "run-ok" {
		t.Fatalf("unexpected inspected result task: %#v", fromResult)
	}

	liveRecorder := httptest.NewRecorder()
	app.routes().ServeHTTP(liveRecorder, httptest.NewRequest(http.MethodGet, "/v1/tasks/live-only", nil))

	if liveRecorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, liveRecorder.Code, liveRecorder.Body.String())
	}
	var liveOnly TaskInfo
	if err := json.NewDecoder(liveRecorder.Body).Decode(&liveOnly); err != nil {
		t.Fatal(err)
	}
	if liveOnly.ID != "live-only" || liveOnly.Status != "running" || liveOnly.Image != "ghcr.io/acme/live:latest" {
		t.Fatalf("unexpected live-only task: %#v", liveOnly)
	}
}

func TestHandleTaskLogsReadsAndTailsLog(t *testing.T) {
	store := openTestStore(t)
	logPath := filepath.Join(store.dir, "runs", "run-ok", "run.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.state.Results = []ResultRecord{
		{ID: "result-ok", RunID: "run-ok", Function: "capture", WindowID: "window-ok", Status: WindowSuccess, StartedAt: now, FinishedAt: now, LogPath: logPath},
	}

	app := &App{cfg: Config{StateDir: store.dir}, store: store, executor: &fakeExecutor{}}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/tasks/run-ok/logs?tail=2", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	var response TaskLogResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Log != "two\nthree\n" {
		t.Fatalf("unexpected log %q", response.Log)
	}
}

func TestHandleTaskLogsReadsRelativePath(t *testing.T) {
	store := openTestStore(t)
	logPath := filepath.Join(store.dir, "runs", "run-ok", "run.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("relative log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.state.Results = []ResultRecord{
		{ID: "result-ok", RunID: "run-ok", Function: "capture", WindowID: "window-ok", Status: WindowSuccess, StartedAt: now, FinishedAt: now, LogPath: filepath.Join("runs", "run-ok", "run.log")},
	}

	app := &App{cfg: Config{StateDir: store.dir}, store: store, executor: &fakeExecutor{}}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/tasks/result-ok/logs", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	var response TaskLogResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Log != "relative log\n" || response.LogPath != logPath {
		t.Fatalf("unexpected relative log response: %#v", response)
	}
}

func TestHandleTaskLogsReportsStartupErrorWithoutLog(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	store.state.Results = []ResultRecord{
		{
			ID:         "result-startup-failed",
			RunID:      "run-startup-failed",
			Function:   "capture",
			WindowID:   "window-startup-failed",
			Status:     WindowFailed,
			StartedAt:  now,
			FinishedAt: now,
			Error:      "mkdir runs/run-startup-failed: permission denied",
		},
	}

	app := &App{cfg: Config{StateDir: store.dir}, store: store, executor: &fakeExecutor{}}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/tasks/run-startup-failed/logs", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); !strings.Contains(got, "task failed before log file was created") || !strings.Contains(got, "permission denied") {
		t.Fatalf("unexpected startup log error: %q", got)
	}
}

func TestHandleTaskStartStoresManualWindow(t *testing.T) {
	store := openTestStore(t)
	executor := &fakeExecutor{}
	app := &App{store: store, executor: executor}
	body, err := json.Marshal(TaskStartRequest{
		Name:     "capture",
		Image:    "ghcr.io/acme/capture:latest",
		Payload:  json.RawMessage(`{"camera":"nadir"}`),
		Duration: "2m",
	})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(body)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if len(executor.ensuredImages) != 1 || executor.ensuredImages[0] != "ghcr.io/acme/capture:latest" {
		t.Fatalf("expected image ensure, got %#v", executor.ensuredImages)
	}
	var response TaskStartResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Window.Function != "capture" || response.Window.Status != WindowPending {
		t.Fatalf("unexpected start response: %#v", response)
	}
	if _, ok := store.state.Windows[response.Window.ID]; !ok {
		t.Fatalf("queued window was not stored: %#v", store.state.Windows)
	}
}

func TestHandleTaskStartUsesExistingFunctionWithoutImage(t *testing.T) {
	store := openTestStore(t)
	store.state.Functions["capture"] = FunctionSpec{Name: "capture", Image: "ghcr.io/acme/capture:latest"}
	executor := &fakeExecutor{}
	app := &App{store: store, executor: executor}
	body, err := json.Marshal(TaskStartRequest{
		Name:     "capture",
		Area:     "science",
		Payload:  json.RawMessage(`{"mode":"survey"}`),
		Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(body)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if len(executor.ensuredImages) != 0 {
		t.Fatalf("expected existing function start not to ensure image, got %#v", executor.ensuredImages)
	}
	var response TaskStartResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Window.Function != "capture" || response.Window.Area != "science" || string(response.Window.Payload) != `{"mode":"survey"}` {
		t.Fatalf("unexpected start response: %#v", response.Window)
	}
}

func TestHandleTaskStartRejectsUnknownFunctionWithoutImage(t *testing.T) {
	store := openTestStore(t)
	app := &App{store: store, executor: &fakeExecutor{}}
	body, err := json.Marshal(TaskStartRequest{Name: "missing"})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(body)))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); !strings.Contains(got, "function \"missing\" not found") {
		t.Fatalf("unexpected start error: %q", got)
	}
}

func TestHandleTaskStopStopsRunningWindow(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	store.state.Windows["window-running"] = WindowRecord{
		WindowSpec:  WindowSpec{ID: "window-running", Function: "capture", Area: "manual", StartAt: now, EndAt: now.Add(time.Minute)},
		Status:      WindowRunning,
		RunID:       "run-running",
		TriggeredAt: &now,
	}
	executor := &fakeExecutor{}
	app := &App{store: store, executor: executor}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/tasks/capture/stop", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if len(executor.stopped) != 1 || executor.stopped[0] != "run-running" {
		t.Fatalf("unexpected stopped tasks: %#v", executor.stopped)
	}
}

func TestHandleTaskStopFallsBackToContainerTask(t *testing.T) {
	store := openTestStore(t)
	executor := &fakeExecutor{}
	app := &App{store: store, executor: executor}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/tasks/live-only/stop", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if len(executor.stopped) != 1 || executor.stopped[0] != "live-only" {
		t.Fatalf("unexpected stopped tasks: %#v", executor.stopped)
	}
}

func TestHandleStatusReturnsDaemonRuntimeState(t *testing.T) {
	store := openTestStore(t)
	store.state.Functions["capture"] = FunctionSpec{Name: "capture", Image: "ghcr.io/acme/capture:latest"}
	executor := &fakeExecutor{containers: []ContainerInfo{{ID: "run-001"}}}
	app := &App{
		cfg:      Config{ContainerdSock: "/run/containerd/containerd.sock"},
		store:    store,
		executor: executor,
	}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	var response StatusResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.State.Functions != 1 || response.Runtime.Containers != 1 || response.Runtime.Namespace != RuntimeNamespace {
		t.Fatalf("unexpected status response: %#v", response)
	}
}

func TestHandleStatusReportsRuntimeError(t *testing.T) {
	store := openTestStore(t)
	app := &App{
		cfg:   Config{ContainerdSock: "/run/containerd/containerd.sock"},
		store: store,
		executor: &fakeExecutor{
			listErr: errors.New("containerd unavailable"),
		},
	}

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	var response StatusResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "ok" || response.Runtime.Error != "containerd unavailable" || response.Runtime.Containers != 0 {
		t.Fatalf("unexpected runtime error response: %#v", response)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}
