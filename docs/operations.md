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

Install the binary, systemd unit, service account, state directory, and host dependencies:

```bash
make build
sudo make install-system PREFIX=/usr/local
```

The install places:

- `orbitald` in `BINDIR`, default `/usr/local/bin`
- `obd` in `CLIBINDIR`, defaulting to `BINDIR`
- `orbitald.service` in `UNITDIR`, default `/usr/local/lib/systemd/system`

The install script logs each step and will install `containerd` when it is missing on hosts with `apt-get`, `dnf`, `yum`, `zypper`, `pacman`, or `apk`. Set `INSTALL_DEPS=0` to skip dependency installation.

By default it configures the systemd service to run as `root`:

- state directory `/var/lib/orbitald`, owned by `root:root` with mode `0750`
- `User=root` and `Group=root` in the generated systemd unit

This matches the default rootful containerd deployment model and avoids mount
and snapshotter permission failures during function startup.

Useful overrides:

```bash
sudo make install-system \
  PREFIX=/usr/local \
  CLIBINDIR=/usr/local/bin \
  ORBITALD_STATE_DIR=/var/lib/orbitald \
  ORBITALD_SNAPSHOTTER=overlayfs \
  CONTAINERD_SOCK=/run/containerd/containerd.sock
```

Set `DESTDIR` to stage files for packaging without installing dependencies or touching systemd:

```bash
make install DESTDIR=/tmp/orbitald-package-root
```

Then enable the service with `sudo systemctl enable --now orbitald`.

The checked-in systemd unit is a template. The install script rewrites `ExecStart` and `WorkingDirectory` before installing it.

- [hack/orbitald.service](../hack/orbitald.service)

## CLI

Global flags go before the command. Use `--addr` for a non-default daemon URL and `--json` for machine-readable output.

```bash
obd --addr http://10.0.0.25:8080 status
obd --json task list
```

### `obd version`

Check the CLI version and, when reachable, the daemon version.

```bash
obd version
```

### `obd status`

Check daemon health, stored state counts, and runtime availability.

```bash
obd status
```

### `obd help`

Print command summaries. Pass a command group for focused help.

```bash
obd help
obd help task
obd help image
```

### `obd images`

List registered runnable images.

```bash
obd images
```

### `obd image inspect NAME`

Inspect one registered runnable image and its related windows/results.

```bash
obd image inspect capture
```

### `obd task list [TASK_NAME]`

List task records by task ID and task name. Pass a task name to filter the list.

```bash
obd task list
obd task list capture
```

Use the `TASK ID` from this output with `inspect`, `logs`, and `stop`.

### `obd task inspect TARGET`

Inspect one task. Use a `TASK ID` from `obd task list`.

```bash
obd task inspect capture-20260905t120000-000001
```

### `obd task describe TARGET`

Alias for `obd task inspect TARGET`.

```bash
obd task describe capture-20260905t120000-000001
```

### `obd task logs TARGET [--tail N]`

Print stored task logs. Use `--tail` to read only the last N lines.

```bash
obd task logs capture-20260905t120000-000001
obd task logs capture-20260905t120000-000001 --tail 100
```

### `obd task start NAME [flags]`

Queue a manual task. `NAME` is the task name shown by `obd task list`.

Start a task that is already registered:

```bash
obd task start capture --payload '{"camera":"nadir","mode":"survey"}'
```

Start a task for a specific area and keep the manual window open for two minutes:

```bash
obd task start capture \
  --area zone-a \
  --duration 2m \
  --payload '{"camera":"nadir"}'
```

Register or update the runnable image before starting:

```bash
obd task start capture \
  --image ghcr.io/acme/capture:latest \
  --payload '{"camera":"nadir"}' \
  --duration 2m \
  --run-timeout 90s \
  --memory 128Mi
```

Run as a specific user when registering or updating the image:

```bash
obd task start capture \
  --image ghcr.io/acme/capture:latest \
  --user 1000 \
  --payload '{"camera":"nadir"}'
```

Read payload JSON from a file:

```bash
obd task start capture --payload @payload.json
```

Example `payload.json`:

```json
{
  "camera": "nadir",
  "mode": "survey"
}
```

Override command args and environment when registering or updating the image:

```bash
obd task start capture \
  --image ghcr.io/acme/capture:latest \
  --arg /app/capture \
  --arg survey \
  --env MODE=survey \
  --env CAMERA=nadir
```

Return the queued task as JSON:

```bash
obd --json task start capture \
  --image ghcr.io/acme/capture:latest \
  --payload '{"camera":"nadir"}'
```

### `obd task stop TARGET`

Stop a running task. Use a `TASK ID` from `obd task list`.

```bash
obd task stop capture-20260905t120000-000001
```

Stop workflow:

```bash
obd task list
obd task stop capture-20260905t120000-000001
obd task inspect capture-20260905t120000-000001
```

## Uninstall

Remove the service, installed binaries, and systemd unit:

```bash
sudo make uninstall
```

The uninstall script logs each step, removes the obsolete containerd socket permission drop-in from older installs if present, and asks before removing the `containerd` package. It preserves `/var/lib/orbitald` by default.

Useful overrides:

```bash
sudo make uninstall REMOVE_CONTAINERD=1 PURGE_STATE=1
sudo make uninstall REMOVE_CONTAINERD=0
sudo make uninstall ORBITALD_STATE_DIR=/srv/orbitald
```

Set `DESTDIR` to remove staged files only:

```bash
make uninstall DESTDIR=/tmp/orbitald-package-root
```

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
