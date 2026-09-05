# API

All responses are JSON. Timestamps are UTC RFC3339 values.

Operator endpoints expose persisted orbitald state and, when containerd is reachable, live containers in the `orbitald` namespace. Contact sync remains the Earth/node data-plane endpoint; task start and stop are operator write endpoints for manual runs and recovery.

## `GET /healthz`

Returns basic daemon health.

### Response

```json
{
  "status": "ok",
  "version": "dev",
  "node_time": "2026-09-03T12:00:00Z"
}
```

## `GET /v1/status`

Returns daemon health, state counts, and container runtime status.

### Response

```json
{
  "status": "ok",
  "version": "dev",
  "node_time": "2026-09-03T12:00:00Z",
  "state": {
    "functions": 2,
    "windows": {
      "pending": 1,
      "running": 0,
      "success": 3,
      "failed": 1,
      "expired": 0
    },
    "results": 4,
    "pending_upload": 1
  },
  "runtime": {
    "namespace": "orbitald",
    "socket": "/run/containerd/containerd.sock",
    "containers": 1
  }
}
```

If containerd cannot be queried, the endpoint still returns `200 OK` and sets `runtime.error`.

## `POST /v1/contact/sync`

It is used to:

- register or update functions
- push future execution windows
- acknowledge results already uploaded to Earth
- fetch still-pending results

### Request

```json
{
  "functions": [
    {
      "name": "capture",
      "image": "ghcr.io/acme/capture:2026-09-03",
      "command": ["/app/capture"],
      "env": {
        "MODE": "survey"
      },
      "user": "1000",
      "memory_limit": "128Mi",
      "timeout": "2m"
    }
  ],
  "windows": [
    {
      "id": "pass-2026-09-03T12:00:00Z",
      "function": "capture",
      "area": "zone-7",
      "start_at": "2026-09-03T12:00:00Z",
      "end_at": "2026-09-03T12:02:00Z",
      "payload": {
        "camera": "nadir"
      }
    }
  ],
  "replace_windows": true,
  "ack_result_ids": [
    "result-20260903t120000-000001"
  ]
}
```

### Rules

- `functions[].name` is required
- `functions[].image` is required
- `timeout` must be a valid Go duration such as `30s` or `2m`
- `memory_limit` must be a valid quantity such as `128Mi`
- `windows[].id` is required
- `windows[].function` must reference a known function
- `windows[].start_at` and `windows[].end_at` are UTC timestamps
- `windows[].payload` must be valid JSON when present

### Response

```json
{
  "version": "dev",
  "node_time": "2026-09-03T12:00:00Z",
  "functions": [],
  "windows": [],
  "pending_results": []
}
```

### `pending_results`

Each pending result includes:

- result metadata
- bounded log text
- paths for payload, output, and log on the node

Results remain pending until their IDs are sent back in `ack_result_ids`.

## `GET /v1/images`

Returns registered runnable function specs.

### Response

```json
[
  {
    "name": "capture",
    "image": "ghcr.io/acme/capture:2026-09-03",
    "command": ["/app/capture"],
    "env": {
      "MODE": "survey"
    },
    "user": "1000",
    "memory_limit": "128Mi",
    "timeout": "2m"
  }
]
```

## `GET /v1/images/{name}`

Returns one function spec and the windows/results associated with that function.

### Response

```json
{
  "function": {
    "name": "capture",
    "image": "ghcr.io/acme/capture:2026-09-03"
  },
  "windows": [],
  "results": []
}
```

Returns `404 Not Found` when the function is not registered.

## `GET /v1/tasks`

Returns task-oriented execution state. A task can come from a stored window/result or from a live container that does not have matching persisted state.

Optional query parameters:

- `function`: only include tasks for the named function. Live containers without matching persisted state are omitted when this filter is set.

### Response

```json
{
  "tasks": [
    {
      "id": "capture-20260903t120000-000001",
      "function": "capture",
      "status": "stopped",
      "image": "ghcr.io/acme/capture:2026-09-03",
      "container_id": "capture-20260903t120000-000001",
      "window_id": "pass-2026-09-03T12:00:00Z",
      "window_status": "success",
      "run_id": "capture-20260903t120000-000001",
      "result_id": "result-20260903t120000-000001",
      "result_status": "success",
      "exit_code": 0,
      "area": "zone-7",
      "started_at": "2026-09-03T12:00:01Z",
      "finished_at": "2026-09-03T12:00:08Z",
      "payload_path": "/var/lib/orbitald/runs/capture-20260903t120000-000001/payload.json",
      "output_dir": "/var/lib/orbitald/runs/capture-20260903t120000-000001/output",
      "log_path": "/var/lib/orbitald/runs/capture-20260903t120000-000001/run.log"
    }
  ]
}
```

Statuses are normalized for operators:

- `pending`: a window is queued
- `running`: a window has been claimed and has a run ID
- `stopped`: the run completed successfully
- `error`: the window or result failed
- `expired`: the window closed before execution

If containerd cannot be queried, the endpoint still returns persisted tasks and sets `runtime_error`.

## `POST /v1/tasks`

Queues a manual task by creating a short-lived window that is immediately eligible to run.

### Request

```json
{
  "name": "capture",
  "image": "ghcr.io/acme/capture:2026-09-03",
  "area": "manual",
  "payload": {
    "camera": "nadir"
  },
  "duration": "10m",
  "memory": "128Mi",
  "run_timeout": "2m",
  "user": "1000",
  "command": ["/app/capture"],
  "env": {
    "MODE": "survey"
  }
}
```

### Rules

- `name` is required
- if `image` is omitted, `name` must already reference a registered function
- if `image` is provided, the function is registered or updated before the window is queued
- `payload` defaults to `{}`
- `area` defaults to `manual`
- `duration` defaults to `10m` and must be positive
- `run_timeout` and `memory` are applied to the function only when `image` is provided

### Response

```json
{
  "version": "dev",
  "node_time": "2026-09-03T12:00:00Z",
  "window": {
    "id": "manual-capture-20260903t120000z",
    "function": "capture",
    "area": "manual",
    "start_at": "2026-09-03T11:59:59Z",
    "end_at": "2026-09-03T12:10:00Z",
    "payload": {
      "camera": "nadir"
    },
    "status": "pending"
  }
}
```

## `GET /v1/tasks/{target}`

Returns one task. `target` may be a task ID, run ID, window ID, result ID, or a live container ID in the `orbitald` namespace.

Returns `404 Not Found` when no stored task or live container matches.

## `GET /v1/tasks/{target}/logs`

Returns the stored task log.

Optional query parameters:

- `tail`: return only the last N log lines. The value must be a non-negative integer.

### Response

```json
{
  "id": "capture-20260903t120000-000001",
  "log_path": "/var/lib/orbitald/runs/capture-20260903t120000-000001/run.log",
  "log": "function output\n"
}
```

Returns `400 Bad Request` when `tail` is invalid or the task failed before a log file was created.

## `POST /v1/tasks/{target}/stop`

Stops a running task. `target` may be a function name, window ID, run ID, or direct live container ID.

### Response

```json
{
  "stopped": [
    "capture-20260903t120000-000001"
  ]
}
```

Returns `404 Not Found` when no matching running task or live container can be stopped.

## `GET /v1/state`

Returns the full local snapshot:

- functions
- windows
- results

Use this for diagnostics and field inspection.
