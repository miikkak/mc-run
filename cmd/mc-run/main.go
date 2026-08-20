// Command mc-run supervises a Minecraft server process: it relays console
// input from a named pipe and stdin, and shuts the server down gracefully
// on SIGTERM.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/miikkak/mc-run/internal/procinfo"
	"github.com/miikkak/mc-run/internal/rcon"
	"github.com/miikkak/mc-run/internal/supervisor"
)

var version = "dev"

const procRoot = "/proc"

func main() {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "pid":
		os.Exit(runPID(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "uid":
		os.Exit(runID("Uid"))
	case len(os.Args) > 1 && os.Args[1] == "gid":
		os.Exit(runID("Gid"))
	default:
		os.Exit(run())
	}
}

// runPID implements `mc-run pid --name <comm>`: it prints the PID of the
// single process whose /proc/<pid>/comm matches name, for images that
// don't ship pgrep/ps. It fails if zero or more than one process matches —
// callers that need one unambiguous target (e.g. jcmd) must not guess.
func runPID(args []string) int {
	fs := flag.NewFlagSet("mc-run pid", flag.ContinueOnError)
	name := fs.String("name", "", "process comm name to search for (required)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  mc-run pid --name <comm>\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *name == "" {
		fmt.Fprintln(os.Stderr, "mc-run pid: --name is required")
		return 2
	}

	pid, err := procinfo.FindPIDByComm(procRoot, *name)
	if err != nil {
		if errors.Is(err, procinfo.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "mc-run pid: no process named %q\n", *name)
		} else {
			fmt.Fprintf(os.Stderr, "mc-run pid: %v\n", err)
		}
		return 1
	}
	fmt.Println(pid)
	return 0
}

// runID implements `mc-run uid` / `mc-run gid`: it prints the real
// uid/gid that PID 1 (the container's main process) is running as, read
// from /proc/1/status instead of `id`/`ps` (works without procps).
func runID(field string) int {
	value, err := procinfo.StatusField(procRoot, 1, field)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mc-run %s: %v\n", strings.ToLower(field), err)
		return 1
	}
	fmt.Println(value)
	return 0
}

func run() int {
	var (
		namedPipe         = flag.String("named-pipe", "", "path to create as a FIFO for console input")
		stopCommand       = flag.String("stop-command", "stop", "command sent to the server on shutdown")
		stopDuration      = flag.Duration("stop-duration", 60*time.Second, "time to wait after sending the stop command before SIGKILL-ing the server")
		stopAnnounceDelay = flag.Duration("stop-server-announce-delay", 0, "if set, announce shutdown and wait this long before sending the stop command")
		showVersion       = flag.Bool("version", false, "print version and exit")
	)
	flag.BoolVar(showVersion, "v", false, "print version and exit (shorthand)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "mc-run supervises a Minecraft server process: it relays console\ninput from a named pipe and stdin, and shuts the server down gracefully\non SIGTERM.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [flags] -- <server command> [args...]\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return 0
	}

	argv := flag.Args()
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "mc-run: no server command given")
		return 2
	}

	opts := supervisor.Options{
		NamedPipePath:           *namedPipe,
		StopCommand:             *stopCommand,
		StopDuration:            *stopDuration,
		StopServerAnnounceDelay: *stopAnnounceDelay,
		EnableRCON:              strings.EqualFold(os.Getenv("ENABLE_RCON"), "true"),
		RCON: rcon.Config{
			Port:       os.Getenv("RCON_PORT"),
			Password:   os.Getenv("RCON_PASSWORD"),
			ConfigFile: os.Getenv("RCON_CONFIG_FILE"),
		},
		Logger: slog.Default(),
	}

	sup := supervisor.New(opts)
	code, err := sup.Run(context.Background(), argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mc-run: %v\n", err)
		return 1
	}
	return code
}
