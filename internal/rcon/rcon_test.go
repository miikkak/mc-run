package rcon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeRconCLI writes a stub rcon-cli script to a temp dir and prepends
// it to PATH for the duration of the test.
func withFakeRconCLI(t *testing.T, script string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "rcon-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if Available() {
		t.Fatal("Available() = true with empty PATH, want false")
	}

	withFakeRconCLI(t, "exit 0\n")
	if !Available() {
		t.Fatal("Available() = false with rcon-cli on PATH, want true")
	}
}

func TestSendStop_Success(t *testing.T) {
	withFakeRconCLI(t, "exit 0\n")

	if err := SendStop(context.Background(), Config{Port: "25575", Password: "secret"}, "stop"); err != nil {
		t.Fatalf("SendStop: %v", err)
	}
}

func TestSendStop_Failure(t *testing.T) {
	withFakeRconCLI(t, "echo 'connection refused' >&2\nexit 1\n")

	err := SendStop(context.Background(), Config{Port: "25575", Password: "secret"}, "stop")
	if err == nil {
		t.Fatal("expected error from failing rcon-cli, got nil")
	}
}

func TestSendStop_NotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if err := SendStop(context.Background(), Config{}, "stop"); err == nil {
		t.Fatal("expected error when rcon-cli is not on PATH, got nil")
	}
}

// TestSendStop_CredentialsNotInArgs guards against regressing to passing
// RCON_PASSWORD as a CLI flag, which would leak it to any other process on
// the host via /proc/<pid>/cmdline or `ps`. The password must reach rcon-cli
// only through its environment.
func TestSendStop_CredentialsNotInArgs(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	envFile := filepath.Join(dir, "env")
	// argv and env are captured to separate files so the argv assertion
	// below can't accidentally pass just because the password also shows
	// up (correctly) in the env dump — the two must be checked
	// independently, not as one combined blob.
	withFakeRconCLI(t, "printf '%s\\n' \"$@\" > "+argvFile+"\nenv | grep ^RCON_ > "+envFile+"\n")

	cfg := Config{Port: "25575", Password: "s3cr3t"}
	if err := SendStop(context.Background(), cfg, "stop"); err != nil {
		t.Fatalf("SendStop: %v", err)
	}

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(argv), cfg.Password) {
		t.Fatalf("password leaked into argv:\n%s", argv)
	}
	if strings.Contains(string(argv), "--password") {
		t.Fatalf("--password flag used, argv:\n%s", argv)
	}

	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(env), "RCON_PORT=25575") || !strings.Contains(string(env), "RCON_PASSWORD=s3cr3t") {
		t.Fatalf("expected RCON_PORT/RCON_PASSWORD in child env, got:\n%s", env)
	}
}

// TestSendStop_ConfigFileUsesFlag guards against passing ConfigFile via a
// RCON_CONFIG environment variable: rcon-cli's --config flag sets a plain Go
// variable directly rather than going through viper's automatic env
// binding, so RCON_CONFIG is silently ignored by the real binary — the
// config file path must be passed as a --config flag instead.
func TestSendStop_ConfigFileUsesFlag(t *testing.T) {
	out := filepath.Join(t.TempDir(), "invocation")
	withFakeRconCLI(t, "printf '%s\\n' \"$@\" > "+out+"\n")

	cfg := Config{ConfigFile: "/data/.rcon-cli.yaml"}
	if err := SendStop(context.Background(), cfg, "stop"); err != nil {
		t.Fatalf("SendStop: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), "--config\n"+cfg.ConfigFile) {
		t.Fatalf("expected --config %s in argv, invocation:\n%s", cfg.ConfigFile, got)
	}
}
