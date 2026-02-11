package ws

import (
	"sync"

	"github.com/gorilla/websocket"
)

type Client struct {
	Hub    *Hub
	Conn   *websocket.Conn
	Send   chan []byte
	Room   string
	IsShop bool // true = shop view, false = family/list view
}

type Hub struct {
	// Registered clients by Room (Family Token)
	Rooms      map[string]map[*Client]bool
	Broadcast  chan Message
	Register   chan *Client
	Unregister chan *Client
	mu         sync.Mutex
}

// Message carries a room, the payload, and optionally targets only one view type.
// If TargetShop is nil, the message is sent to ALL clients in the room.
// If TargetShop is non-nil, only clients whose IsShop matches *TargetShop receive it.
type Message struct {
	Room       string
	Payload    []byte
	TargetShop *bool // nil = broadcast to all, &true = shop only, &false = family only
}

func NewHub() *Hub {
	return &Hub{
		Rooms:      make(map[string]map[*Client]bool),
		Broadcast:  make(chan Message),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
	}
}

func (h *Hub) Run() {
	for {
		select {
			case client := <-h.Register:
				h.mu.Lock()
				if h.Rooms[client.Room] == nil {
					h.Rooms[client.Room] = make(map[*Client]bool)
				}
				h.Rooms[client.Room][client] = true
				h.mu.Unlock()

			case client := <-h.Unregister:
				h.mu.Lock()
				if clients, ok := h.Rooms[client.Room]; ok {
					if _, ok := clients[client]; ok {
						delete(clients, client)
						close(client.Send)
						if len(clients) == 0 {
							delete(h.Rooms, client.Room)
						}
					}
				}
				h.mu.Unlock()

			case message := <-h.Broadcast:
				h.mu.Lock()
				if clients, ok := h.Rooms[message.Room]; ok {
					for client := range clients {
						// Skip clients that aren't the intended target view
						if message.TargetShop != nil && client.IsShop != *message.TargetShop {
							continue
						}
						select {
			case client.Send <- message.Payload:
			default:
				close(client.Send)
				delete(clients, client)
						}
					}
					if len(clients) == 0 {
						delete(h.Rooms, message.Room)
					}
				}
				h.mu.Unlock()
		}
	}
}

func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister <- c
		c.Conn.Close()
	}()
	for {
		_, _, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *Client) WritePump() {
	defer func() {
		c.Conn.Close()
	}()
	for {
		select {
			case message, ok := <-c.Send:
				if !ok {
					c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				w, err := c.Conn.NextWriter(websocket.TextMessage)
				if err != nil {
					return
				}
				w.Write(message)
				if err := w.Close(); err != nil {
					return
				}
		}
	}
}

// Helper to make a bool pointer — use with TargetShop
func BoolPtr(b bool) *bool { return &b }
