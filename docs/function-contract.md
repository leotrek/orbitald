# Function Contract

Each function is an OCI image executed once for a matching pass window.

## Inputs

The container receives these environment variables:

- `ORBITALD_WINDOW_ID`
- `ORBITALD_FUNCTION`
- `ORBITALD_AREA`
- `ORBITALD_PAYLOAD_PATH`
- `ORBITALD_OUTPUT_DIR`

## Mounted paths

### Payload

`/var/run/orbitald/payload.json`

This file contains the JSON payload attached to the window.

### Output

`/var/run/orbitald/output`

This directory is writable. Use it for files that need to be collected later.

## Logging

Write normal run logs to stdout or stderr.

`orbitald` stores both streams in one log file per run.

## Exit behavior

- exit code `0`: success
- non-zero exit code: failure
- timeout: failure

## Recommended image shape

Keep images small and deterministic.

Prefer:

- one static binary or one small runtime
- explicit command entrypoint
- bounded memory usage
- bounded runtime

Avoid:

- background daemons
- large mutable local state
- assuming permanent network access

## Minimal example behavior

1. read the payload JSON
2. perform the capture or analysis
3. write summary logs to stdout
4. write artifacts to the output directory
5. exit
