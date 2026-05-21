package signals

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/redis/go-redis/v9"
)

// Event is an application signal (name + opaque JSON payload).
type Event struct {
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}

// Handler runs when an event is emitted in-process.
type Handler func(ctx context.Context, e Event) error

// Bus dispatches named signals to registered handlers (synchronous).
type Bus struct {
	mu   sync.RWMutex
	subs map[string][]Handler
}

func NewBus() *Bus {
	return &Bus{subs: make(map[string][]Handler)}
}

// On registers a handler for an event name. Multiple handlers per name run in registration order.
func (b *Bus) On(name string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[name] = append(b.subs[name], h)
}

// Emit runs all handlers for e.Name. Handlers should not block indefinitely.
func (b *Bus) Emit(ctx context.Context, e Event) error {
	b.mu.RLock()
	handlers := append([]Handler(nil), b.subs[e.Name]...)
	b.mu.RUnlock()

	var firstErr error
	for _, h := range handlers {
		if h == nil {
			continue
		}
		if herr := h(ctx, e); herr != nil {
			if firstErr == nil {
				firstErr = herr
			}
			log.Printf("signals: handler for %q: %v", e.Name, herr)
		}
	}
	return firstErr
}

// Broadcast publishes the event to Redis for other instances or workers to observe.
// It does not invoke local handlers (use Emit for local + optionally Broadcast for fan-out).
func Broadcast(ctx context.Context, rdb *redis.Client, channel string, e Event) error {
	if rdb == nil || channel == "" {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = rdb.Publish(ctx, channel, b).Result()
	return err
}

// StartSubscriber listens for Broadcast payloads on channel and calls fn for each message.
func StartSubscriber(ctx context.Context, rdb *redis.Client, channel string, fn func(context.Context, Event) error) {
	if rdb == nil || channel == "" {
		return
	}
	sub := rdb.Subscribe(ctx, channel)
	go func() {
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok || msg == nil {
					return
				}
				var e Event
				if err := json.Unmarshal([]byte(msg.Payload), &e); err != nil {
					log.Printf("signals: redis subscriber decode: %v", err)
					continue
				}
				if err := fn(ctx, e); err != nil {
					log.Printf("signals: redis subscriber handler: %v", err)
				}
			}
		}
	}()
}
