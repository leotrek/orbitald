# Architecture

`orbitald` is a single-node execution daemon.

The target flow is:

`window deploy -> local pass detector -> function run -> local result store -> upload on next contact`

## Components

### Earth side

Earth is responsible for:

- building function images
- making images reachable to the node during a contact window
- sending function definitions
- sending execution windows
- acknowledging uploaded results

Earth is not in the execution path when the node is over the monitoring area.

### Node side

The node runs one daemon with four jobs:

1. receive sync requests over HTTP
2. store function and window state on disk
3. watch UTC time and trigger due windows
4. execute the matching container and store the result

## Internal modules

### API server

The API surface is small:

- `POST /v1/contact/sync`
- `GET /v1/state`
- `GET /healthz`

### Store

The store persists:

- function specs
- execution windows
- result records

State is stored in one JSON file under the state directory.

### Scheduler

The scheduler polls current UTC time at a fixed interval.

For each window it decides:

- `pending`: not started yet
- `running`: claimed for execution
- `success`: completed with exit code `0`
- `failed`: started but ended with an error or non-zero exit
- `expired`: window closed before execution started

### Executor

The executor uses `containerd` directly.

For each run it:

1. checks that the image is already available locally
2. creates a temporary container and snapshot
3. mounts the payload file and output directory
4. starts the task
5. waits for exit or timeout
6. stores logs and metadata
7. removes the task and container

Execution is always cold start:

`0 -> 1 -> 0`

There are no warm replicas and no horizontal scaling.

## State layout

Default state directory:

`/var/lib/orbitald`

Important paths:

- `state.json`: persisted control-plane state
- `runs/<run-id>/payload.json`: input payload for one run
- `runs/<run-id>/output/`: files produced by one run
- `runs/<run-id>/run.log`: combined stdout/stderr for one run

## Contact model

During a contact window:

1. Earth syncs function definitions
2. Earth syncs future pass windows
3. Earth fetches pending results
4. Earth acknowledges result IDs it has stored safely

This makes the node fully autonomous between contacts.
