package sse

import (
	"fmt"
	"net/http"
	"sync"
)

// Event represents a server-sent event.
type Event struct {
	Type string
	Data string
	ID   string
}

// Client represents a connected SSE client.
type Client struct {
	ID     string
	Events chan Event
}

// Broker manages SSE connections and fan-out.
type Broker struct {
	mu      sync.RWMutex
	clients map[string]map[string]*Client // topic -> clientID -> client
}

// NewBroker creates a new SSE broker.
func NewBroker() *Broker {
	return &Broker{
		clients: make(map[string]map[string]*Client),
	}
}

// Subscribe adds a client to a topic.
func (b *Broker) Subscribe(topic, clientID string) *Client {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.clients[topic]; !ok {
		b.clients[topic] = make(map[string]*Client)
	}

	client := &Client{
		ID:     clientID,
		Events: make(chan Event, 64),
	}
	b.clients[topic][clientID] = client
	return client
}

// Unsubscribe removes a client from a topic.
func (b *Broker) Unsubscribe(topic, clientID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if clients, ok := b.clients[topic]; ok {
		if client, ok := clients[clientID]; ok {
			close(client.Events)
			delete(clients, clientID)
		}
		if len(clients) == 0 {
			delete(b.clients, topic)
		}
	}
}

// Publish sends an event to all clients subscribed to a topic.
func (b *Broker) Publish(topic string, event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if clients, ok := b.clients[topic]; ok {
		for _, client := range clients {
			select {
			case client.Events <- event:
			default:
				// Client buffer full, skip
			}
		}
	}
}

// ServeHTTP handles an SSE connection for a given topic and client.
func ServeHTTP(w http.ResponseWriter, r *http.Request, client *Client) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for {
		select {
		case event, ok := <-client.Events:
			if !ok {
				return
			}
			if event.ID != "" {
				fmt.Fprintf(w, "id: %s\n", event.ID)
			}
			// Don't emit "event:" field — the JSON data already contains a "type" key,
			// and EventSource.onmessage only fires for unnamed events.
			fmt.Fprintf(w, "data: %s\n\n", event.Data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
