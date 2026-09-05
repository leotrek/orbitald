package orbitald

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/v1/contact/sync", a.handleSync)
	mux.HandleFunc("/v1/status", a.handleStatus)
	mux.HandleFunc("/v1/state", a.handleState)
	mux.HandleFunc("/v1/images", a.handleImages)
	mux.HandleFunc("/v1/images/", a.handleImage)
	mux.HandleFunc("/v1/tasks", a.handleTasks)
	mux.HandleFunc("/v1/tasks/", a.handleTask)
	return withAccessLog(mux)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"version":   Version,
		"node_time": time.Now().UTC(),
	})
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, a.store.Snapshot())
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := a.store.Snapshot()
	runtimeStatus := RuntimeStatus{
		Namespace: RuntimeNamespace,
		Socket:    a.cfg.ContainerdSock,
	}
	containers, err := a.executor.ListContainers(r.Context())
	if err != nil {
		runtimeStatus.Error = err.Error()
	} else {
		runtimeStatus.Containers = len(containers)
	}

	writeJSON(w, http.StatusOK, StatusResponse{
		Status:   "ok",
		Version:  Version,
		NodeTime: snapshot.NodeTime,
		State:    StateCountsFromSnapshot(snapshot),
		Runtime:  runtimeStatus,
	})
}

func (a *App) handleImages(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/images" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, a.store.Snapshot().Functions)
}

func (a *App) handleImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name, ok := trimSinglePathValue(r.URL.Path, "/v1/images/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	details, ok := FunctionDetailsFromState(a.store.Snapshot(), name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, details)
}

func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/tasks" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.handleTaskList(w, r)
	case http.MethodPost:
		a.handleTaskStart(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleTask(w http.ResponseWriter, r *http.Request) {
	parts, ok := trimPathParts(r.URL.Path, "/v1/tasks/")
	if !ok || len(parts) == 0 || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}

	target := parts[0]
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		a.handleTaskInspect(w, r, target)
	case len(parts) == 2 && parts[1] == "logs" && r.Method == http.MethodGet:
		a.handleTaskLogs(w, r, target)
	case len(parts) == 2 && parts[1] == "stop" && r.Method == http.MethodPost:
		a.handleTaskStop(w, r, target)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleTaskList(w http.ResponseWriter, r *http.Request) {
	function := r.URL.Query().Get("function")
	tasks := BuildTaskInfos(a.store.Snapshot(), function)
	response := TaskListResponse{Tasks: tasks}

	containers, err := a.executor.ListContainers(r.Context())
	if err != nil {
		response.RuntimeError = err.Error()
	} else {
		response.Tasks = MergeContainerInfos(tasks, containers, function)
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleTaskInspect(w http.ResponseWriter, r *http.Request, target string) {
	info, ok := FindTaskInfo(a.store.Snapshot(), target)
	if !ok {
		container, err := a.executor.ContainerInfo(r.Context(), target)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, taskFromContainer(container))
		return
	}

	if info.RunID != "" {
		if container, err := a.executor.ContainerInfo(r.Context(), info.RunID); err == nil {
			info.ContainerID = container.ID
			if info.Image == "" {
				info.Image = container.Image
			}
		}
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *App) handleTaskLogs(w http.ResponseWriter, r *http.Request, target string) {
	tail, err := parseTailQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, ok := FindTaskInfo(a.store.Snapshot(), target)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if TaskFailedBeforeLog(info) {
		http.Error(w, "task failed before log file was created: "+info.Error, http.StatusBadRequest)
		return
	}
	logPath := LogPathForTask(a.cfg.StateDir, info)
	if logPath == "" {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data = TailLog(data, tail)
	writeJSON(w, http.StatusOK, TaskLogResponse{
		ID:      info.ID,
		LogPath: logPath,
		Log:     string(data),
	})
}

func (a *App) handleTaskStart(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request TaskStartRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	response, err := a.startTask(r, request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleTaskStop(w http.ResponseWriter, r *http.Request, target string) {
	stopped, err := a.stopTask(r, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, TaskStopResponse{Stopped: stopped})
}

func (a *App) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()
	var request SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, fn := range request.Functions {
		if err := validateFunction(fn); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := a.executor.EnsureImage(r.Context(), fn.Image); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := a.store.UpsertFunctions(request.Functions); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.store.UpsertWindows(request.Windows, request.ReplaceWindows); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.store.AckResults(request.AckResultIDs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pending, err := a.store.PendingResults(a.cfg.MaxLogBytes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	snapshot := a.store.Snapshot()
	writeJSON(w, http.StatusOK, SyncResponse{
		Version:        Version,
		NodeTime:       snapshot.NodeTime,
		Functions:      snapshot.Functions,
		Windows:        snapshot.Windows,
		PendingResults: pending,
	})
}

func (a *App) startTask(r *http.Request, request TaskStartRequest) (TaskStartResponse, error) {
	if request.Name == "" {
		return TaskStartResponse{}, errors.New("task name is required")
	}
	area := request.Area
	if area == "" {
		area = "manual"
	}
	duration := 10 * time.Minute
	if request.Duration != "" {
		parsed, err := time.ParseDuration(request.Duration)
		if err != nil {
			return TaskStartResponse{}, err
		}
		duration = parsed
	}
	if duration <= 0 {
		return TaskStartResponse{}, errors.New("duration must be positive")
	}

	payload := []byte("{}")
	if len(request.Payload) > 0 {
		if !json.Valid(request.Payload) {
			return TaskStartResponse{}, errors.New("payload must be valid JSON")
		}
		payload = append([]byte(nil), request.Payload...)
	}

	fn, exists := a.store.Function(request.Name)
	if request.Image != "" {
		if !exists {
			fn = FunctionSpec{Name: request.Name}
		}
		fn.Image = request.Image
		if len(request.Command) > 0 {
			fn.Command = append([]string(nil), request.Command...)
		}
		if len(request.Env) > 0 {
			fn.Env = copyStringMap(request.Env)
		}
		if request.Memory != "" {
			fn.MemoryLimit = request.Memory
		}
		if request.RunTimeout != "" {
			fn.Timeout = request.RunTimeout
		}
		if request.User != "" {
			fn.User = request.User
		}
		if err := validateFunction(fn); err != nil {
			return TaskStartResponse{}, err
		}
		if err := a.executor.EnsureImage(r.Context(), fn.Image); err != nil {
			return TaskStartResponse{}, err
		}
		if err := a.store.UpsertFunctions([]FunctionSpec{fn}); err != nil {
			return TaskStartResponse{}, err
		}
	} else if !exists {
		return TaskStartResponse{}, errors.New("function " + strconv.Quote(request.Name) + " not found; pass --image to register it before starting")
	}

	now := time.Now().UTC()
	window := WindowSpec{
		ID:       manualWindowID(request.Name, now),
		Function: request.Name,
		Area:     area,
		StartAt:  now.Add(-1 * time.Second),
		EndAt:    now.Add(duration),
		Payload:  payload,
	}
	if err := a.store.UpsertWindows([]WindowSpec{window}, false); err != nil {
		return TaskStartResponse{}, err
	}

	snapshot := a.store.Snapshot()
	for _, item := range snapshot.Windows {
		if item.ID == window.ID {
			return TaskStartResponse{
				Version:  Version,
				NodeTime: snapshot.NodeTime,
				Window:   item,
			}, nil
		}
	}
	return TaskStartResponse{}, errors.New("queued task was not found in state")
}

func (a *App) stopTask(r *http.Request, target string) ([]string, error) {
	snapshot := a.store.Snapshot()
	matches := []WindowRecord{}
	for _, window := range snapshot.Windows {
		if window.Status != WindowRunning {
			continue
		}
		if window.Function == target || window.ID == target || window.RunID == target {
			matches = append(matches, window)
		}
	}

	stopped := []string{}
	if len(matches) == 0 {
		if err := a.executor.StopContainerTask(r.Context(), target); err != nil {
			return nil, err
		}
		return []string{target}, nil
	}

	for _, window := range matches {
		if window.RunID == "" {
			return nil, errors.New("running window " + strconv.Quote(window.ID) + " has no run id")
		}
		if err := a.executor.StopContainerTask(r.Context(), window.RunID); err != nil {
			return nil, err
		}
		stopped = append(stopped, window.RunID)
	}
	return stopped, nil
}

func taskFromContainer(container ContainerInfo) TaskInfo {
	return TaskInfo{
		ID:          container.ID,
		Status:      NormalizeContainerTaskStatus(container.TaskStatus),
		Image:       container.Image,
		ContainerID: container.ID,
		RunID:       container.ID,
		StartedAt:   timePtr(container.CreatedAt),
	}
}

func parseTailQuery(r *http.Request) (int, error) {
	value := r.URL.Query().Get("tail")
	if value == "" {
		return -1, nil
	}
	tail, err := strconv.Atoi(value)
	if err != nil || tail < 0 {
		return 0, errors.New("tail must be a non-negative integer")
	}
	return tail, nil
}

func trimSinglePathValue(path, prefix string) (string, bool) {
	parts, ok := trimPathParts(path, prefix)
	if !ok || len(parts) != 1 {
		return "", false
	}
	return parts[0], true
}

func trimPathParts(path, prefix string) ([]string, bool) {
	trimmed := strings.TrimPrefix(path, prefix)
	if trimmed == path || trimmed == "" {
		return nil, false
	}
	rawParts := strings.Split(trimmed, "/")
	parts := make([]string, 0, len(rawParts))
	for _, raw := range rawParts {
		if raw == "" {
			return nil, false
		}
		part, err := url.PathUnescape(raw)
		if err != nil {
			return nil, false
		}
		parts = append(parts, part)
	}
	return parts, true
}

func copyStringMap(values map[string]string) map[string]string {
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	return copyValues
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if value == nil {
		value = map[string]any{}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	if err := encoder.Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
