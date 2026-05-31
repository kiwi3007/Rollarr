package api

import (
	"fmt"
	"net/http"
	"sync"
)

// Broker fans out refresh signals to all connected SSE clients.
type Broker struct {
	mu      sync.Mutex
	clients map[chan struct{}]struct{}
}

func NewBroker() *Broker {
	return &Broker{clients: make(map[chan struct{}]struct{})}
}

// Notify sends a refresh signal to every connected client. Non-blocking per
// client — a slow or full client is skipped rather than blocking the caller.
func (b *Broker) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// ServeHTTP handles GET /api/events as a Server-Sent Events stream.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := make(chan struct{}, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	fmt.Fprintf(w, "data: connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			fmt.Fprintf(w, "data: refresh\n\n")
			flusher.Flush()
		}
	}
}
