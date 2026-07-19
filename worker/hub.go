package worker

import (
	"fmt"
	"sync"
)

// Hub fans out SSE messages to all connected subscribers. Because every message
// is a full snapshot, a slow subscriber can be dropped for the current message
// without harm — the next snapshot supersedes it.
type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[chan []byte]struct{})}
}

// Subscribe registers a new subscriber and returns its channel. The caller must
// Unsubscribe when done (e.g. on client disconnect).
func (h *Hub) Subscribe() chan []byte {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscriber channel (idempotent).
func (h *Hub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Broadcast delivers msg to every subscriber, skipping any whose buffer is full.
func (h *Hub) Broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default: // slow subscriber: drop this message, the next snapshot follows
		}
	}
}

// SSEMessage formats an event name + JSON data into the SSE wire format. data
// must not contain newlines (json.Marshal output never does).
func SSEMessage(event string, data []byte) []byte {
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, data))
}
