# Example Functions

This directory contains a few small Python example function images for `orbitald`.

Each example follows the runtime contract:

- reads `/var/run/orbitald/payload.json`
- uses `ORBITALD_*` environment variables
- writes files under `/var/run/orbitald/output`
- exits with a clear success or failure status

The images use `python:3.12-alpine` with a single `main.py` file and no extra packages. That is about as small as you can keep a Python-based example here without switching away from Python entirely.

## Included examples

- `payload-copy`: copies the payload into the output directory and writes basic metadata
- `artifact-writer`: creates a few output artifacts and a manifest
- `conditional-fail`: succeeds by default and exits non-zero when the payload asks it to fail

## Build with containerd

These commands build the images into the same `containerd` namespace that `orbitald` uses:

```bash
nerdctl --namespace orbitald build -t orbitald/example-payload-copy:2026-09-03 examples/functions/payload-copy
nerdctl --namespace orbitald build -t orbitald/example-artifact-writer:2026-09-03 examples/functions/artifact-writer
nerdctl --namespace orbitald build -t orbitald/example-conditional-fail:2026-09-03 examples/functions/conditional-fail
```

## Register with orbitald

See [sync-request.json](sync-request.json) for a sample `POST /v1/contact/sync` payload.

The sample uses UTC windows on `2026-09-04`. Adjust those timestamps before using it on a live node.
