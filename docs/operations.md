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
- cleanup of the obsolete containerd socket permission drop-in from older installs

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

Basic system commands:

```bash
obd version
obd status
obd help
```

Function commands:

```bash
obd fn list
obd fn status
obd fn info capture
obd fn start capture --payload '{"camera":"nadir"}'
obd fn start capture --image ghcr.io/acme/capture:latest
obd fn stop capture
```

List commands:

```bash
obd list
obd list capture
obd list runs capture
obd list functions
```

Container runtime commands:

```bash
obd container status
obd container info <run-id>
obd container stop <run-id>
```

Use `--addr` when `orbitald` is listening somewhere other than `http://127.0.0.1:8080`. Use `--containerd-sock` when the socket is not `/run/containerd/containerd.sock`.

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
