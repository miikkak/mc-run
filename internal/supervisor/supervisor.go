// Package supervisor runs a Minecraft server child process, feeding it
// console input from a named pipe and stdin, and shutting it down gracefully
// on request.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/miikkak/mc-run/internal/namedpipe"
	"github.com/miikkak/mc-run/internal/rcon"
)

// Options configures a Supervisor.
type Options struct {
	// NamedPipePath, if non-empty, is created as a FIFO and piped into the
	// child's stdin for the lifetime of the child process.
	NamedPipePath string

	// StopCommand is sent to the child (via RCON or stdin) on SIGTERM.
	StopCommand string

	// StopDuration is how long to wait for the child to exit after sending
	// the stop command before SIGKILL-ing it.
	StopDuration time.Duration

	// StopServerAnnounceDelay, if non-zero, sends a "say" warning and waits
	// this long before sending the stop command.
	StopServerAnnounceDelay time.Duration

	// EnableRCON, when true, attempts to send the stop command via the
	// rcon-cli binary (if present on PATH) before falling back to stdin.
	EnableRCON bool
	RCON       rcon.Config

	Logger *slog.Logger
}

// Supervisor runs and manages a single child process.
type Supervisor struct {
	opts Options

	mu    sync.Mutex
	stdin io.WriteCloser
}

// New creates a Supervisor with the given options, filling in defaults for
// unset fields.
func New(opts Options) *Supervisor {
	if opts.StopCommand == "" {
		opts.StopCommand = "stop"
	}
	if opts.StopDuration <= 0 {
		opts.StopDuration = 60 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Supervisor{opts: opts}
}

// Write sends p to the child's stdin. It implements io.Writer so a
// Supervisor can be used directly as the destination for the named-pipe
// pump and the stdin passthrough goroutine.
func (s *Supervisor) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin == nil {
		return 0, io.ErrClosedPipe
	}
	return s.stdin.Write(p)
}

func (s *Supervisor) writeLine(line string) {
	if _, err := s.Write([]byte(line + "\n")); err != nil {
		s.opts.Logger.Warn("failed to write to child stdin", "error", err)
	}
}

// Run spawns argv[0] with argv[1:] as its arguments, supervises it, and
// blocks until it exits. It returns the child's exit code.
func (s *Supervisor) Run(ctx context.Context, argv []string) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("supervisor: no command given")
	}

	if s.opts.NamedPipePath != "" {
		if err := namedpipe.Ensure(s.opts.NamedPipePath); err != nil {
			return 0, fmt.Errorf("supervisor: %w", err)
		}
		defer func() {
			if err := os.Remove(s.opts.NamedPipePath); err != nil && !os.IsNotExist(err) {
				s.opts.Logger.Warn("failed to remove named pipe", "path", s.opts.NamedPipePath, "error", err)
			}
		}()
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Run the child as its own process group leader so a stop-duration
	// timeout can kill the whole tree (see killChild below), not just the
	// direct child — a shell wrapper or launcher script that forked the
	// actual JVM would otherwise survive as an orphan.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, fmt.Errorf("supervisor: stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("supervisor: start: %w", err)
	}

	s.mu.Lock()
	s.stdin = stdin
	s.mu.Unlock()

	pumpCtx, cancelPumps := context.WithCancel(ctx)
	defer cancelPumps()

	if s.opts.NamedPipePath != "" {
		go func() {
			if err := namedpipe.Pump(pumpCtx, s.opts.NamedPipePath, s); err != nil {
				s.opts.Logger.Warn("named pipe pump exited", "error", err)
			}
		}()
	}

	go func() {
		_, _ = io.Copy(s, os.Stdin)
	}()

	sigCh := make(chan os.Signal, 1)
	// SIGTERM is what container runtimes send on shutdown; SIGINT is what
	// an attached terminal sends on Ctrl+C during interactive use (e.g.
	// `podman run -it` / `podman attach`) — both should trigger the same
	// graceful stop rather than SIGINT falling through to Go's default
	// immediate-termination handling.
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	// mc-run runs as PID 1 in the container. Go's default SIGQUIT handling
	// dumps goroutine stacks and terminates the process, so an accidental
	// SIGQUIT (e.g. a jcmd/kill invocation meant for the java child but
	// misdirected at PID 1) would take the whole container down. Trap and
	// log it instead of letting the runtime default apply. This is a no-op
	// by design: unlike SIGTERM below, SIGQUIT triggers no action and is
	// never forwarded to the child raw.
	sigQuitCh := make(chan os.Signal, 1)
	signal.Notify(sigQuitCh, syscall.SIGQUIT)
	defer signal.Stop(sigQuitCh)
	go func() {
		for {
			select {
			case <-sigQuitCh:
				s.opts.Logger.Warn("received SIGQUIT, ignoring")
			case <-pumpCtx.Done():
				return
			}
		}
	}()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case err := <-waitCh:
		return exitCodeFrom(err), nil
	case sig := <-sigCh:
		s.opts.Logger.Info("received signal, stopping server", "signal", sig, "stop_duration", s.opts.StopDuration)
		return s.gracefulStop(ctx, cmd, waitCh)
	case <-ctx.Done():
		// ctx is already done, so a fresh context is used for the stop
		// sequence itself (announce delay, RCON timeout) rather than one
		// that would return immediately.
		s.opts.Logger.Info("context cancelled, stopping server", "stop_duration", s.opts.StopDuration)
		return s.gracefulStop(context.Background(), cmd, waitCh)
	}
}

// gracefulStop sends the stop command via stop, then waits up to
// StopDuration for the child to exit before killing it. The StopDuration
// timer starts immediately, in parallel with stop() rather than after it
// returns: stop() can block for a while (an unresponsive RCON connection, or
// writing to a child that has stopped reading its stdin), and that must
// never itself delay the kill deadline meant to bound exactly that kind of
// hang.
func (s *Supervisor) gracefulStop(ctx context.Context, cmd *exec.Cmd, waitCh <-chan error) (int, error) {
	// stop()'s own context, cancelled when gracefulStop returns by either
	// branch below, so the goroutine can't outlive this call: stop() already
	// selects on ctx.Done() during its announce-delay wait and passes ctx
	// through to the RCON attempt's timeout, so cancelling it here unblocks
	// whichever of those it's currently in rather than leaving it running in
	// the background after the child (and the point of stopping it) is gone.
	stopCtx, cancelStop := context.WithCancel(ctx)
	defer cancelStop()
	go s.stop(stopCtx)

	timer := time.NewTimer(s.opts.StopDuration)
	defer timer.Stop()

	select {
	case err := <-waitCh:
		return exitCodeFrom(err), nil
	case <-timer.C:
		s.opts.Logger.Warn("stop duration exceeded, killing child process")
		killChild(cmd, s.opts.Logger)
		return exitCodeFrom(<-waitCh), nil
	}
}

// killChild SIGKILLs the child's whole process group (see Setpgid in Run),
// not just the direct child PID — a shell wrapper or launcher script that
// forked the actual server process would otherwise be left running as an
// orphan. Falls back to killing just the direct child if the process-group
// kill fails (e.g. the process already exited).
func killChild(cmd *exec.Cmd, logger *slog.Logger) {
	pgid := cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		logger.Warn("failed to kill child process group, falling back to killing child pid only", "pgid", pgid, "error", err)
		_ = cmd.Process.Kill()
	}
}

// stop sends the configured stop command to the child, preferring RCON when
// enabled and available, falling back to writing directly to stdin.
func (s *Supervisor) stop(ctx context.Context) {
	if s.opts.StopServerAnnounceDelay > 0 {
		s.writeLine(fmt.Sprintf("say Server shutting down in %ds", int(s.opts.StopServerAnnounceDelay.Seconds())))

		// ctx.Done() only cuts the wait short; it does not skip sending the
		// stop command below — an early-cancelled ctx must not leave the
		// server running with the announcement as its only warning.
		timer := time.NewTimer(s.opts.StopServerAnnounceDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}

	if s.opts.EnableRCON && rcon.Available() {
		rconCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		err := rcon.SendStop(rconCtx, s.opts.RCON, s.opts.StopCommand)
		if err == nil {
			return
		}
		s.opts.Logger.Warn("rcon stop failed, falling back to stdin", "error", err)
	}

	s.writeLine(s.opts.StopCommand)
}

// exitCodeFrom translates a cmd.Wait() error into a shell-style exit code,
// using the 128+signal convention for signal-terminated processes (e.g. 137
// for a SIGKILL from the OOM killer) rather than Go's default -1.
func exitCodeFrom(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
		return exitErr.ExitCode()
	}

	return 1
}
