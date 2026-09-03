# orbitald

`orbitald` is a small offline-first function runner for one node.

It is built for this path:

`window deploy -> local pass detector -> function run -> local result store -> upload on next contact`

The daemon keeps only what this flow needs:

- function deployment metadata
- preloaded container images in `containerd`
- pass windows sent from Earth
- one local scheduler
- one local result store
- one sync endpoint for the next contact window

It does not include:

- a gateway compatibility layer
- queue workers
- Telemtry/ Prometheus
- multi-replica autoscaling
- clustering

## Runtime model

Each function is just an OCI image plus a small spec.

During a contact window, Earth sends:

1. function specs
2. pass windows with UTC `start_at` and `end_at`
3. acknowledgements for results already received

When a pass window opens, `orbitald`:

1. matches current UTC time against stored windows
2. starts the function container once
3. mounts:
   - `/var/run/orbitald/payload.json`
   - `/var/run/orbitald/output`
4. stores run metadata and logs locally
5. returns pending results on the next sync call

Functions are not kept warm. Execution is always `0 -> 1 -> 0`.

## API

### `POST /v1/contact/sync`

This is the only write API you need.

Example request:

```json
{
  "functions": [
    {
      "name": "capture",
      "image": "ghcr.io/acme/capture:2026-09-03",
      "memory_limit": "128Mi",
      "timeout": "2m"
    }
  ],
  "windows": [
    {
      "id": "pass-2026-09-03T12:00:00Z",
      "function": "capture",
      "area": "monitoring-zone-7",
      "start_at": "2026-09-03T12:00:00Z",
      "end_at": "2026-09-03T12:02:00Z",
      "payload": {
        "camera": "nadir",
        "mode": "survey"
      }
    }
  ],
  "replace_windows": true,
  "ack_result_ids": []
}
```

Example response:

```json
{
  "version": "dev",
  "node_time": "2026-09-03T12:00:00Z",
  "functions": [],
  "windows": [],
  "pending_results": []
}
```

### `GET /v1/state`

Returns the stored functions, windows, and result metadata.

### `GET /healthz`

Basic health response.

## Function contract

Your container can read:

- `ORBITALD_WINDOW_ID`
- `ORBITALD_FUNCTION`
- `ORBITALD_AREA`
- `ORBITALD_PAYLOAD_PATH`
- `ORBITALD_OUTPUT_DIR`

The simplest approach is:

1. read `/var/run/orbitald/payload.json`
2. do the work
3. write the important result to stdout
4. optionally write files under `/var/run/orbitald/output`

## Examples

Example function images and a sample sync payload live under [examples/functions](examples/functions/README.md).

## Build

```bash
go build ./...
```

## Run

```bash
sudo ./orbitald -listen :8080 -state-dir /var/lib/orbitald
```

## Docs

- [Architecture](docs/architecture.md)
- [API](docs/api.md)
- [Function Contract](docs/function-contract.md)
- [Operations](docs/operations.md)
