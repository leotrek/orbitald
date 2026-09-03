# API

## `POST /v1/contact/sync`

This is the only write endpoint.

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

## `GET /v1/state`

Returns the full local snapshot:

- functions
- windows
- results

Use this for diagnostics and field inspection.

## `GET /healthz`

Returns:

- `status`
- `version`
- `node_time`
