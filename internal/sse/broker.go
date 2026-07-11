package sse

import (
	"fmt"
	"sync"
	"time"
)

// SSEEvent represents a server-sent event to be broadcast to clients.
type SSEEvent struct {
	EventType string
	Data      []byte
}

// Client represents a single SSE subscriber connection.
type Client struct {
	ID     string
	Events chan SSEEvent
}

// Broker manages SSE client subscriptions and broadcasts.
type Broker struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewBroker creates a new SSE broker.
func NewBroker() *Broker {
	return &Broker{
		clients: make(map[string]*Client),
	}
}

// Subscribe registers a new client and returns it.
func (b *Broker) Subscribe() *Client {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := fmt.Sprintf("sse-%d", time.Now().UnixNano())
	client := &Client{
		ID:     id,
		Events: make(chan SSEEvent, 64),
	}
	b.clients[id] = client
	return client
}

// Unsubscribe removes a client, closes its event channel, and signals done.
func (b *Broker) Unsubscribe(client *Client) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.clients[client.ID]; ok {
		delete(b.clients, client.ID)
		close(client.Events)
	}
}

// Broadcast sends an event to all connected clients.
// If a client's buffer is full, the message is dropped for that client
// to avoid blocking the publisher.
func (b *Broker) Broadcast(eventType string, data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, client := range b.clients {
		select {
		case client.Events <- SSEEvent{EventType: eventType, Data: data}:
		default:
		}
	}
}
