// Package namedpipe manages a FIFO used to feed console commands into a
// supervised child process's stdin.
package namedpipe

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"
)

// Ensure creates path as a FIFO if it does not already exist. If path exists
// and is not a FIFO, it returns an error rather than clobbering it.
func Ensure(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.Mode()&os.ModeNamedPipe == 0 {
			return fmt.Errorf("namedpipe: %s exists and is not a FIFO", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("namedpipe: stat %s: %w", path, err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return fmt.Errorf("namedpipe: mkfifo %s: %w", path, err)
	}
	return nil
}

// Pump copies data written to the FIFO at path into dst until ctx is
// cancelled. The FIFO is opened O_RDWR so the read end never observes EOF
// from a departing writer (holding it open as our own implicit writer),
// which avoids needing a reopen loop on every writer disconnect.
func Pump(ctx context.Context, path string, dst io.Writer) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("namedpipe: open %s: %w", path, err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = f.Close()
		case <-done:
		}
	}()
	defer close(done)

	_, err = io.Copy(dst, f)
	if ctx.Err() != nil {
		return nil
	}
	return err
}
