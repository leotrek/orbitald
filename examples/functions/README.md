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

## Build with Docker

If you prefer plain Docker commands, build the example images like this:

```bash
docker build -t orbitald/example-payload-copy:2026-09-03 examples/functions/payload-copy
docker build -t orbitald/example-artifact-writer:2026-09-03 examples/functions/artifact-writer
docker build -t orbitald/example-conditional-fail:2026-09-03 examples/functions/conditional-fail
```

`orbitald` runs functions from the `orbitald` `containerd` namespace, not from Docker's local image store.

If you built the image with Docker on the same node, import it into `containerd` before calling the sync API. Run these commands as a user that can access the `containerd` socket:

```bash
docker save orbitald/example-payload-copy:2026-09-03 | ctr --namespace orbitald images import -
docker save orbitald/example-artifact-writer:2026-09-03 | ctr --namespace orbitald images import -
docker save orbitald/example-conditional-fail:2026-09-03 | ctr --namespace orbitald images import -
```

## Build with containerd

These commands build the images into the same `containerd` namespace that `orbitald` uses:

```bash
nerdctl --namespace orbitald build -t orbitald/example-payload-copy:2026-09-03 examples/functions/payload-copy
nerdctl --namespace orbitald build -t orbitald/example-artifact-writer:2026-09-03 examples/functions/artifact-writer
nerdctl --namespace orbitald build -t orbitald/example-conditional-fail:2026-09-03 examples/functions/conditional-fail
```

Use this path when `orbitald` and your image build both happen on the same node.

## Push the function to orbitald

There is no separate "upload function source" API.

You push a function to `orbitald` by sending a function spec that points at an OCI image:

- local image already imported into `containerd`
- or a registry image such as `ghcr.io/acme/payload-copy:2026-09-03`

If you want the node to pull from a registry, tag and push the image first:

```bash
docker build -t ghcr.io/acme/payload-copy:2026-09-03 examples/functions/payload-copy
docker push ghcr.io/acme/payload-copy:2026-09-03
```

Then set `functions[].image` in the sync payload to that registry reference.

## Register with orbitald

See [sync-request.json](sync-request.json) for a sample `POST /v1/contact/sync` payload.

The sample uses UTC windows on `2026-09-04`. Adjust those timestamps before using it on a live node.

Once the image reference is correct, register the functions and windows with:

```bash
curl -X POST http://127.0.0.1:8080/v1/contact/sync \
  -H 'Content-Type: application/json' \
  --data @examples/functions/sync-request.json
```

That single call does three things:

1. validates each `functions[].image`
2. stores the function specs
3. stores the pass windows that will trigger each run

To confirm that the function is present on the node, inspect local state:

```bash
curl http://127.0.0.1:8080/v1/state
```
