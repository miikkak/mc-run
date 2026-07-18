# mc-run

A minimal, stdlib-only Go process supervisor for Minecraft server containers.

`mc-run` replaces [`itzg/mc-server-runner`](https://github.com/itzg/mc-server-runner) in
[`mc-server-container`](https://github.com/miikkak/mc-server-container). It is **not a fork** —
it's a from-scratch implementation covering exactly what that container uses: process
supervision, named-pipe console input, and graceful shutdown on `SIGTERM`.

## Why not just use mc-server-runner?

`mc-server-runner`'s used portion is already lean, but it ships attack surface and dependencies
this container never needs: an SSH remote console, a websocket console, and non-stdlib
dependencies (`gliderlabs/ssh`, `coder/websocket`, `zap`, `uuid`, `go-flagsfiller`) that only
exist to support those unused features. `mc-run` drops all of that: Go stdlib only, no `go.mod`
`require` lines.

## What it does

1. Spawns the given command (e.g. `java <opts> -jar server.jar --nogui`) as a child process,
   with the child's stdout/stderr inherited from `mc-run`'s own.
2. If `--named-pipe` is set, creates that path as a FIFO (if it doesn't already exist) and
   relays anything written to it into the child's stdin — this is how
   `scripts/mc-send-to-console.sh` sends console commands, and `scripts/mc-health.sh` checks the
   FIFO's existence as a liveness signal.
3. Also relays `mc-run`'s own stdin to the child's stdin concurrently, so `podman attach` /
   interactive use keeps working.
4. On `SIGTERM`:
   - If `ENABLE_RCON=TRUE` and `rcon-cli` is on `PATH`, sends the stop command via RCON.
   - Otherwise (or if the RCON attempt fails), writes the stop command directly to the child's
     stdin.
   - If `--stop-server-announce-delay` is set, sends a `say` warning and waits that long first.
   - Waits up to `--stop-duration` (default `60s`) for the child to exit, then sends `SIGKILL`.
5. Exits with the child's exit code (using the `128+signal` convention for signal-terminated
   children, e.g. `137` for a `SIGKILL`/OOM-kill), and removes the named pipe on the way out.

## Flags

| Flag                           | Default | Description                                                          |
| ------------------------------ | ------- | -------------------------------------------------------------------- |
| `--named-pipe`                 | `""`    | Path to create as a FIFO for console input. Disabled if unset.       |
| `--stop-command`               | `stop`  | Command sent to the server on shutdown.                              |
| `--stop-duration`              | `60s`   | Time to wait after sending the stop command before `SIGKILL`.        |
| `--stop-server-announce-delay` | `0`     | If set, `say` a shutdown warning and wait this long before stopping. |
| `--version`                    |         | Print version and exit.                                              |

Everything after the flags is executed as the child command, e.g.:

```sh
mc-run --named-pipe /tmp/minecraft-console --stop-duration 60s -- \
  java -Xmx4G -jar server.jar --nogui
```

## RCON environment variables

Read directly from the environment (matching `entrypoint.sh`'s existing variables), not passed
as flags:

- `ENABLE_RCON` — `TRUE` to attempt RCON-based stop before falling back to stdin.
- `RCON_PORT`, `RCON_PASSWORD` — used when `RCON_CONFIG_FILE` is unset.
- `RCON_CONFIG_FILE` — if set, passed to `rcon-cli --config <file>` instead of port/password.

## Building

```sh
make build
```

Static Linux binary, no CGO:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o mc-run ./cmd/mc-run
```

## Out of scope

SSH/websocket remote console, `-shell`, `-detach-stdin`, `-bootstrap`, and the `SIGUSR1`
announce-delay bypass are intentionally not ported — none are referenced anywhere in
`mc-server-container`'s `scripts/` or README. They can be added later if an actual need shows up.
