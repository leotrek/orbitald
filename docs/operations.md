# Operations

## Build

```bash
make test
make build
```

Cross-build release binaries:

```bash
make dist
```

## Run locally

```bash
./bin/orbitald -listen :8080 -state-dir ./var/orbitald
```

## Run on the node

`orbitald` expects:

- Linux
- `containerd`
- a writable state directory
- required function images already present locally or reachable during contact

Example:

```bash
sudo /usr/local/bin/orbitald -listen :8080 -state-dir /var/lib/orbitald
```

Systemd unit:

- [hack/orbitald.service](../hack/orbitald.service)

## Contact window workflow

During contact:

1. ensure the required images are available to the node
2. call `POST /v1/contact/sync`
3. read back `pending_results`
4. persist the results on Earth
5. send the received result IDs back in the next `ack_result_ids`

## Failure handling

If the daemon restarts:

- running windows are moved back to `pending` when still inside the time window
- overdue running windows become failed or expired depending on time

If a function times out:

- the task is terminated
- the result is stored as failed

If a window closes before the scheduler starts it:

- the window becomes `expired`

## Practical limits for a Raspberry Pi 3

Recommended defaults:

- `max-concurrent=1`
- small images
- strict `memory_limit`
- short timeouts

Treat the node as a single-runner appliance, not as a general-purpose cluster.
