package sse_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/sse"
)

func TestBrokerSubscribePublish(t *testing.T) {
	b := sse.NewBroker()

	client := b.Subscribe("scan-123", "client-1")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.ID != "client-1" {
		t.Fatalf("expected client ID client-1, got %s", client.ID)
	}

	// Publish an event
	b.Publish("scan-123", sse.Event{
		Type: "progress",
		Data: `{"percent":50}`,
		ID:   "evt-1",
	})

	select {
	case evt := <-client.Events:
		if evt.Type != "progress" {
			t.Fatalf("expected event type progress, got %s", evt.Type)
		}
		if evt.Data != `{"percent":50}` {
			t.Fatalf("unexpected event data: %s", evt.Data)
		}
		if evt.ID != "evt-1" {
			t.Fatalf("expected event ID evt-1, got %s", evt.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	b.Unsubscribe("scan-123", "client-1")
}

func TestBrokerUnsubscribe(t *testing.T) {
	b := sse.NewBroker()

	client := b.Subscribe("topic", "c1")
	b.Unsubscribe("topic", "c1")

	// Channel should be closed
	_, ok := <-client.Events
	if ok {
		t.Fatal("expected channel to be closed after unsubscribe")
	}
}

func TestBrokerFanOut(t *testing.T) {
	b := sse.NewBroker()

	c1 := b.Subscribe("topic", "c1")
	c2 := b.Subscribe("topic", "c2")
	c3 := b.Subscribe("other-topic", "c3")

	b.Publish("topic", sse.Event{Type: "test", Data: "hello"})

	// c1 and c2 should get the event
	for _, c := range []*sse.Client{c1, c2} {
		select {
		case evt := <-c.Events:
			if evt.Data != "hello" {
				t.Fatalf("unexpected data: %s", evt.Data)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for event on topic subscriber")
		}
	}

	// c3 should NOT get the event
	select {
	case <-c3.Events:
		t.Fatal("c3 should not receive events from other topic")
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	b.Unsubscribe("topic", "c1")
	b.Unsubscribe("topic", "c2")
	b.Unsubscribe("other-topic", "c3")
}

func TestBrokerConcurrency(t *testing.T) {
	b := sse.NewBroker()
	var wg sync.WaitGroup

	// Concurrent subscribe/publish/unsubscribe
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			clientID := strings.Replace(time.Now().String(), " ", "", -1)
			c := b.Subscribe("stress", clientID)
			b.Publish("stress", sse.Event{Data: "ping"})
			<-c.Events
			b.Unsubscribe("stress", clientID)
		}(i)
	}

	wg.Wait()
}

func TestBrokerDisconnect(t *testing.T) {
	b := sse.NewBroker()
	client := b.Subscribe("stream", "viewer")

	// Publish event
	b.Publish("stream", sse.Event{Type: "data", Data: `{"msg":"test"}`})

	// Drain event
	select {
	case <-client.Events:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	// Unsubscribe simulates cleanup on disconnect
	b.Unsubscribe("stream", "viewer")

	// Publishing after unsubscribe should not panic
	b.Publish("stream", sse.Event{Data: "after-disconnect"})
}

func TestServeHTTP(t *testing.T) {
	b := sse.NewBroker()
	client := b.Subscribe("test-topic", "test-client")

	// Send events then close
	go func() {
		time.Sleep(10 * time.Millisecond)
		b.Publish("test-topic", sse.Event{Type: "msg", Data: "hello", ID: "1"})
		time.Sleep(10 * time.Millisecond)
		b.Unsubscribe("test-topic", "test-client")
	}()

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	w := httptest.NewRecorder()

	sse.ServeHTTP(w, req, client)

	body := w.Body.String()
	// ServeHTTP intentionally omits the "event:" field — the JSON data carries its own "type" key.
	if !strings.Contains(body, "data: hello") {
		t.Fatalf("expected 'data: hello' in SSE output, got: %s", body)
	}
	if !strings.Contains(body, "id: 1") {
		t.Fatalf("expected 'id: 1' in SSE output, got: %s", body)
	}
}
