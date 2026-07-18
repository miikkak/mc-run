package namedpipe

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// syncBuffer wraps bytes.Buffer with a mutex so it can be written to by the
// Pump goroutine and read from the test goroutine concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestEnsureCreatesFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console")

	if err := Ensure(path); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("expected %s to be a FIFO, mode = %v", path, info.Mode())
	}

	// Calling Ensure again on an existing FIFO must be a no-op, not an error.
	if err := Ensure(path); err != nil {
		t.Fatalf("Ensure (idempotent): %v", err)
	}
}

func TestEnsureRejectsNonFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console")
	if err := os.WriteFile(path, []byte("not a fifo"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Ensure(path); err == nil {
		t.Fatal("expected Ensure to reject a regular file, got nil error")
	}
}

func TestPumpRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console")
	if err := Ensure(path); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var dst syncBuffer
	pumpErr := make(chan error, 1)
	go func() { pumpErr <- Pump(ctx, path, &dst) }()

	// No sleep needed here: opening a FIFO O_WRONLY blocks until a reader
	// has opened it, so this call itself waits for Pump's O_RDWR open above
	// rather than racing it.
	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open for write: %v", err)
	}
	if _, err := w.WriteString("say hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for dst.String() != "say hello\n" {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for data, got %q", dst.String())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-pumpErr:
		if err != nil {
			t.Fatalf("Pump returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Pump did not return after context cancellation")
	}
}
