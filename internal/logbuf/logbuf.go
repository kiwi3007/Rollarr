package logbuf

import (
	"io"
	"log"
	"os"
	"sync"
)

const defaultCap = 500

// Buffer is a thread-safe ring buffer that captures log lines and also
// forwards them to an underlying writer (os.Stderr by default).
type Buffer struct {
	mu    sync.Mutex
	lines []string
	head  int // next write position
	size  int // number of valid entries
	cap   int
}

var global = &Buffer{cap: defaultCap, lines: make([]string, defaultCap)}

// Install replaces the default logger's output with one that writes to the
// global ring buffer and also forwards to w (pass os.Stderr for normal output).
func Install(w io.Writer) {
	if w == nil {
		w = os.Stderr
	}
	log.SetOutput(io.MultiWriter(w, global))
}

// Write implements io.Writer — called by the stdlib logger for each log line.
func (b *Buffer) Write(p []byte) (int, error) {
	line := string(p)
	// Strip trailing newline for cleaner JSON.
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	b.mu.Lock()
	b.lines[b.head] = line
	b.head = (b.head + 1) % b.cap
	if b.size < b.cap {
		b.size++
	}
	b.mu.Unlock()
	return len(p), nil
}

// Last returns the most recent n log lines in chronological order.
func Last(n int) []string {
	return global.last(n)
}

func (b *Buffer) last(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if n <= 0 || b.size == 0 {
		return nil
	}
	if n > b.size {
		n = b.size
	}

	out := make([]string, n)
	// oldest of the n lines we want
	start := (b.head - n + b.cap) % b.cap
	for i := range out {
		out[i] = b.lines[(start+i)%b.cap]
	}
	return out
}
