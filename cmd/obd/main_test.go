package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/orbitald/orbitald/internal/orbitald"
)

func TestStatusFetchesStatusEndpoint(t *testing.T) {
	now := mustTime(t, "2026-09-05T12:00:00Z")
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/status" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		return orbitald.StatusResponse{
			Status:   "ok",
			Version:  orbitald.Version,
			NodeTime: now,
			State: orbitald.StateCounts{
				Functions:     2,
				Results:       3,
				PendingUpload: 1,
				Windows: orbitald.WindowCounts{
					Pending: 1,
					Running: 1,
					Success: 1,
				},
			},
			Runtime: orbitald.RuntimeStatus{
				Namespace:  orbitald.RuntimeNamespace,
				Socket:     "/run/containerd/containerd.sock",
				Containers: 4,
			},
		}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	if err := cfg.status(nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"daemon: ok", "functions: 2", "pending=1 running=1 success=1", "pending_upload=1", "namespace: orbitald", "containers: 4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected status output to contain %q:\n%s", want, got)
		}
	}
}

func TestImagesFetchesImagesEndpoint(t *testing.T) {
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/images" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		return []orbitald.FunctionSpec{
			{Name: "capture", Image: "ghcr.io/acme/capture:latest"},
			{Name: "analyze", Image: "ghcr.io/acme/analyze:latest"},
		}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	if err := cfg.images(nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "capture") || !strings.Contains(got, "ghcr.io/acme/analyze:latest") {
		t.Fatalf("unexpected image list output:\n%s", got)
	}
}

func TestImageInspectFetchesImageEndpoint(t *testing.T) {
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/images/capture" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		return orbitald.FunctionDetails{
			Function: orbitald.FunctionSpec{Name: "capture", Image: "ghcr.io/acme/capture:latest"},
		}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	if err := cfg.image([]string{"inspect", "capture"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "function: capture") || !strings.Contains(out.String(), "image: ghcr.io/acme/capture:latest") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestTaskDescribeFetchesTaskEndpoint(t *testing.T) {
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tasks/result-ok" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		return orbitald.TaskInfo{
			ID:       "run-ok",
			Function: "capture",
			Status:   "stopped",
			ResultID: "result-ok",
			Image:    "ghcr.io/acme/capture:latest",
		}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	if err := cfg.task([]string{"describe", "result-ok"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "task: run-ok") || !strings.Contains(out.String(), "image: ghcr.io/acme/capture:latest") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestTaskListFetchesFunctionFilter(t *testing.T) {
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tasks" || r.URL.Query().Get("function") != "capture" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		return orbitald.TaskListResponse{
			Tasks: []orbitald.TaskInfo{{ID: "run-ok", Function: "capture", Status: "stopped"}},
		}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	if err := cfg.task([]string{"list", "capture"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "run-ok") || !strings.Contains(out.String(), "capture") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestTaskLogsFetchesTailFromEndpoint(t *testing.T) {
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tasks/run-ok/logs" || r.URL.Query().Get("tail") != "2" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		return orbitald.TaskLogResponse{ID: "run-ok", Log: "two\nthree\n"}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	if err := cfg.taskLogs([]string{"run-ok", "--tail", "2"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "two\nthree\n" {
		t.Fatalf("unexpected logs %q", got)
	}
}

func TestTaskStartPostsStartRequest(t *testing.T) {
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tasks" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var request orbitald.TaskStartRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Name != "capture" || request.Image != "ghcr.io/acme/capture:latest" || request.Duration != "10m0s" {
			t.Fatalf("unexpected request body: %#v", request)
		}
		if string(request.Payload) != `{"camera":"nadir"}` {
			t.Fatalf("unexpected payload %s", request.Payload)
		}
		now := mustTime(t, "2026-09-05T12:00:00Z")
		return orbitald.TaskStartResponse{
			Version:  orbitald.Version,
			NodeTime: now,
			Window: orbitald.WindowRecord{
				WindowSpec: orbitald.WindowSpec{
					ID:       "manual-capture-20260905t120000z",
					Function: "capture",
					Area:     "manual",
					StartAt:  now,
					EndAt:    now.Add(10 * time.Minute),
				},
				Status: orbitald.WindowPending,
			},
		}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	err := cfg.taskStart([]string{"capture", "--image", "ghcr.io/acme/capture:latest", "--payload", `{"camera":"nadir"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "queued manual-capture-20260905t120000z for function capture") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestTaskStartPostsImageOptions(t *testing.T) {
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tasks" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var request orbitald.TaskStartRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Name != "capture" || request.Area != "science" || request.Duration != "30s" {
			t.Fatalf("unexpected base request fields: %#v", request)
		}
		if request.Image != "ghcr.io/acme/capture:latest" || request.Memory != "128Mi" || request.RunTimeout != "20s" || request.User != "1000" {
			t.Fatalf("unexpected image request fields: %#v", request)
		}
		if len(request.Command) != 2 || request.Command[0] != "/app/capture" || request.Command[1] != "survey" {
			t.Fatalf("unexpected command args: %#v", request.Command)
		}
		if request.Env["MODE"] != "survey" || request.Env["CAMERA"] != "nadir" {
			t.Fatalf("unexpected env: %#v", request.Env)
		}
		now := mustTime(t, "2026-09-05T12:00:00Z")
		return orbitald.TaskStartResponse{
			Version:  orbitald.Version,
			NodeTime: now,
			Window: orbitald.WindowRecord{
				WindowSpec: orbitald.WindowSpec{
					ID:       "manual-capture-20260905t120000z",
					Function: "capture",
					Area:     "science",
					StartAt:  now,
					EndAt:    now.Add(30 * time.Second),
				},
				Status: orbitald.WindowPending,
			},
		}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	err := cfg.taskStart([]string{
		"capture",
		"--image", "ghcr.io/acme/capture:latest",
		"--area", "science",
		"--duration", "30s",
		"--memory", "128Mi",
		"--run-timeout", "20s",
		"--user", "1000",
		"--arg", "/app/capture",
		"--arg", "survey",
		"--env", "MODE=survey",
		"--env", "CAMERA=nadir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "queued manual-capture-20260905t120000z for function capture") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestTaskStopPostsStopRequest(t *testing.T) {
	cfg := testCLI(t, func(r *http.Request) (any, int) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tasks/run-ok/stop" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		return orbitald.TaskStopResponse{Stopped: []string{"run-ok"}}, http.StatusOK
	})

	var out bytes.Buffer
	cfg.out = &out
	if err := cfg.taskStop([]string{"run-ok"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "stopped run-ok" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestLegacyAliasesAreUnknown(t *testing.T) {
	for _, command := range []string{"fn", "run", "list", "logs", "container", "tasks", "system"} {
		var out bytes.Buffer
		var errOut bytes.Buffer
		code := run([]string{command}, &out, &errOut)
		if code != 2 {
			t.Fatalf("expected %q to exit 2, got %d", command, code)
		}
		if !strings.Contains(errOut.String(), "unknown command") {
			t.Fatalf("expected unknown command error for %q, got:\n%s", command, errOut.String())
		}
	}
}

func testCLI(t *testing.T, handler func(*http.Request) (any, int)) cli {
	t.Helper()
	return cli{
		endpoint: "http://orbitald.test",
		out:      io.Discard,
		err:      io.Discard,
		httpClient: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				value, status := handler(r)
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{
					StatusCode: status,
					Status:     http.StatusText(status),
					Body:       io.NopCloser(bytes.NewReader(data)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    r,
				}, nil
			}),
		},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
