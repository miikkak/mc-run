// Package rcon shells out to the rcon-cli binary (miikkak/rcon-cli, an
// independent implementation — see its README) to send a stop command to a
// running Minecraft server.
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
// the port and password from Config.
//
// Port and password are passed to the child via its environment
// (RCON_PORT/RCON_PASSWORD), never as command-line flags — a process's argv
// is readable by any other process on the host (e.g. via
// /proc/<pid>/cmdline or `ps`), which would otherwise expose RCON_PASSWORD.
// rcon-cli reads RCON_HOST/RCON_PORT/RCON_PASSWORD via
// viper.SetEnvPrefix("rcon")+AutomaticEnv(), binding to the same "host"/
// "port"/"password" flags SendStop would otherwise have passed on argv.
//
// ConfigFile has no such env var equivalent: rcon-cli's --config flag sets
// a plain Go variable directly (bypassing viper, so it's read before
// AutomaticEnv would apply to it) rather than going through viper's
// automatic env binding, so it's passed as a --config flag instead. That's
// fine — a config file path isn't a secret the way a password is.
func SendStop(ctx context.Context, cfg Config, command string) error {
	path, err := exec.LookPath("rcon-cli")
	if err != nil {
		return fmt.Errorf("rcon: rcon-cli not found on PATH: %w", err)
	}

	env := os.Environ()
	var args []string
	if cfg.ConfigFile != "" {
		args = append(args, "--config", cfg.ConfigFile)
	} else {
		if cfg.Port != "" {
			env = append(env, "RCON_PORT="+cfg.Port)
		}
		if cfg.Password != "" {
			env = append(env, "RCON_PASSWORD="+cfg.Password)
		}
	}
	args = append(args, command)

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env

	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > maxErrorOutput {
			out = append(out[:maxErrorOutput], []byte("... (truncated)")...)
		}
		return fmt.Errorf("rcon: rcon-cli %s: %w (output: %s)", command, err, out)
	}
	return nil
}
