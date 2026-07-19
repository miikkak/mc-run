// Command mc-run supervises a Minecraft server process: it relays console
// input from a named pipe and stdin, and shuts the server down gracefully
// on SIGTERM.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/miikkak/mc-run/internal/rcon"
	"github.com/miikkak/mc-run/internal/supervisor"
)

var version = "dev"

func main() {
	os.Exit(run())
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
