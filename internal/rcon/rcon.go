// Package rcon shells out to the rcon-cli binary to send a stop command to a
// running Minecraft server, mirroring itzg/mc-server-runner's
// hasRconCli()/sendRconCommand() behavior.
package rcon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// maxErrorOutput caps how much of rcon-cli's combined output is included in
// a returned error, so an unexpectedly chatty failure can't flood logs (or,
// in principle, echo back more of the invoking environment than intended).
const maxErrorOutput = 4096

// Config holds the connection details needed to reach the server's RCON
// listener, sourced from the container's ENABLE_RCON/RCON_PORT/
// RCON_PASSWORD/RCON_CONFIG_FILE environment variables.
type Config struct {
	Port       string
	Password   string
	ConfigFile string
}

// Available reports whether the rcon-cli binary can be found on PATH.
func Available() bool {
	_, err := exec.LookPath("rcon-cli")
	return err == nil
}

// SendStop invokes rcon-cli to send command (typically "stop") to the
// server. It prefers RCON_CONFIG_FILE when set, otherwise connects using
// the port and password from Config, passed to the child via its
// environment rather than command-line flags — rcon-cli (itzg/rcon-cli)
// binds RCON_HOST/RCON_PORT/RCON_PASSWORD/RCON_CONFIG automatically, and
// unlike flags, a process's argv is readable by any other process on the
// host (e.g. via /proc/<pid>/cmdline or `ps`), which would otherwise expose
// RCON_PASSWORD.
func SendStop(ctx context.Context, cfg Config, command string) error {
	path, err := exec.LookPath("rcon-cli")
	if err != nil {
		return fmt.Errorf("rcon: rcon-cli not found on PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, path, command)
	cmd.Env = os.Environ()
	if cfg.ConfigFile != "" {
		cmd.Env = append(cmd.Env, "RCON_CONFIG="+cfg.ConfigFile)
	} else {
		if cfg.Port != "" {
			cmd.Env = append(cmd.Env, "RCON_PORT="+cfg.Port)
		}
		if cfg.Password != "" {
			cmd.Env = append(cmd.Env, "RCON_PASSWORD="+cfg.Password)
		}
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > maxErrorOutput {
			out = append(out[:maxErrorOutput], []byte("... (truncated)")...)
		}
		return fmt.Errorf("rcon: rcon-cli %s: %w (output: %s)", command, err, out)
	}
	return nil
}
