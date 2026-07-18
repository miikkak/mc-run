// Package rcon shells out to the rcon-cli binary to send a stop command to a
// running Minecraft server, mirroring itzg/mc-server-runner's
// hasRconCli()/sendRconCommand() behavior.
package rcon

import (
	"context"
	"fmt"
	"os/exec"
)

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
func SendStop(ctx context.Context, cfg Config, command string) error {
	path, err := exec.LookPath("rcon-cli")
	if err != nil {
		return fmt.Errorf("rcon: rcon-cli not found on PATH: %w", err)
	}

	var args []string
	if cfg.ConfigFile != "" {
		args = []string{"--config", cfg.ConfigFile}
	} else {
		args = []string{"--port", cfg.Port, "--password", cfg.Password}
	}
	args = append(args, command)

	cmd := exec.CommandContext(ctx, path, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rcon: rcon-cli %s: %w (output: %s)", command, err, out)
	}
	return nil
}
