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

## About this project

This was built with heavy Claude Code assistance — most of the implementation
is AI-generated, with the design and review driven by me. It has unit test
coverage across every internal package (see `internal/*/*_test.go`) and runs
as PID 1 in my own production Minecraft server stack
([mc-server-container](https://github.com/miikkak/mc-server-container)), so
it sees real day-to-day use, not just its own test suite. Read the source
and file issues if something looks off.

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

`mc-run` itself reads these directly from its own environment (matching `entrypoint.sh`'s
existing variables) — they're not `mc-run` flags. It then invokes
[`rcon-cli`](https://github.com/miikkak/rcon-cli), an independent implementation (not itzg's)
built for this stack:

- `ENABLE_RCON` — `TRUE` to attempt RCON-based stop before falling back to stdin.
- `RCON_PORT`, `RCON_PASSWORD` — used when `RCON_CONFIG_FILE` is unset. Passed to `rcon-cli` via
  its own `RCON_PORT`/`RCON_PASSWORD` environment variables (which it reads via
  `viper.AutomaticEnv()`), never as command-line flags, so they don't end up readable via
  `/proc/<pid>/cmdline` or `ps`.
- `RCON_CONFIG_FILE` — if set, passed to `rcon-cli` as its `--config <file>` flag instead of
  port/password. Unlike port/password, `rcon-cli` doesn't expose an env var for this (`--config`
  sets a plain variable directly rather than going through its viper env binding), and a file
  path isn't a secret the way a password is, so a flag is fine here.

## Building

```sh
make build
```

Static Linux binary, no CGO:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o mc-run ./cmd/mc-run
```

## Helper subcommands

Minimal-footprint container images don't ship `pgrep`/`ps`, so scripts that `podman exec`
into a running container previously scanned `/proc` by hand. `mc-run` already runs as PID 1
with native `/proc` access, so it exposes that as subcommands instead:

| Command                    | Description                                                                                                                                                                          |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `mc-run pid --name <comm>` | Prints the PID of the single process whose `/proc/<pid>/comm` equals `<comm>` (e.g. `java`). Exit 1 if zero or more than one process matches — it never guesses which one you meant. |
| `mc-run uid`               | Prints the real UID that PID 1 (the container's main process) runs as, from `/proc/1/status`.                                                                                        |
| `mc-run gid`               | Same, for the real GID.                                                                                                                                                              |

These read-only lookups have no flags of their own beyond `pid`'s `--name`, and don't touch
the supervisor, RCON, or named-pipe machinery — they're meant to be run via
`podman exec <container> mc-run <subcommand>` from outside the container.

## `update-plugins`: Velocity's missing update folder

PaperMC servers copy jars from `plugins/update/` over `plugins/` on startup, matching by the
plugin name declared inside each jar. Velocity has no equivalent
([PaperMC/Velocity#809](https://github.com/PaperMC/Velocity/issues/809)), so `mc-run` replays
the same sequence before the JVM starts:

```sh
mc-run update-plugins --dir /data/plugins
```

For each jar directly in `<dir>/update`, its declared Velocity plugin `id` (read from the
`velocity-plugin.json` entry inside the jar, not the filename) is matched against the `id` of a
jar directly in `<dir>`. On a match: the old jar's content is replaced with the update's, the
result is renamed to the update jar's filename, and the update-folder source is removed —
matching Paper's `io.papermc.paper.plugin.provider.source.FileProviderSource#checkUpdate`
(id-based matching, not the legacy CraftBukkit filename-based match). A jar that can't be
opened as a zip, has no `velocity-plugin.json`, or has no matching counterpart is skipped and
logged; it never aborts the rest of the run.

## `install-plugins`: adding a brand-new plugin

Neither Paper's `plugins/update/` nor `update-plugins` above can install a plugin that has no
already-installed counterpart — both only ever replace a jar that's already in `plugins/`. A
`plugins/install/` folder gets a brand-new jar into `plugins/` before the JVM starts, applied
alongside `update-plugins` ([#77](https://github.com/miikkak/mc-run/issues/77)):

```sh
mc-run install-plugins --dir /data/plugins --server-type velocity
mc-run install-plugins --dir /data/plugins --server-type paper
```

For each jar directly in `<dir>/install`, mc-run checks whether it declares a descriptor for
`--server-type` before copying it: `velocity-plugin.json` for `velocity`, `plugin.yml` or
`paper-plugin.yml` for `paper`. This is a presence check, not an exclusivity check — a fat jar
bundling descriptors for several platforms (a common pattern) still matches on each platform it
declares. On a match, the jar is copied into `<dir>` under its own filename and removed from
`install/`. A jar with no matching descriptor, a jar that can't be opened as a zip, or a jar
whose filename already exists in `<dir>` (use `plugins/update/` for that case instead) is
skipped and logged; it never aborts the rest of the run.

## Out of scope

SSH/websocket remote console, `-shell`, `-detach-stdin`, `-bootstrap`, and the `SIGUSR1`
announce-delay bypass are intentionally not ported — none are referenced anywhere in
`mc-server-container`'s `scripts/` or README. They can be added later if an actual need shows up.
