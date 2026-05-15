package hub

import "sync"

type EventHub struct {
	mu   sync.RWMutex
	subs map[string][]chan []byte
}

func New() *EventHub {
	return &EventHub{subs: make(map[string][]chan []byte)}
}

func (h *EventHub) Subscribe(key string) chan []byte {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[key] = append(h.subs[key], ch)
	h.mu.Unlock()
	return ch
}

func (h *EventHub) Unsubscribe(key string, ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.subs[key]
	for i, s := range subs {
		if s == ch {
			h.subs[key] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	close(ch)
}

func (h *EventHub) Publish(key string, data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range h.subs[key] {
		select {
		case ch <- data:
		default:
		}
	}
}
