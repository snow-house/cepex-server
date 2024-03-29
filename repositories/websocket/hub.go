package websocket

import "github.com/gorilla/websocket"

type Connection struct {
	Ws   *websocket.Conn
	Send chan []byte
}

type Message struct {
	Data []byte
	Room string
}

type Subscription struct {
	Conn *Connection
	Room string
}

type Hub struct {
	Rooms      map[string]map[*Connection]bool
	Broadcast  chan Message
	Register   chan Subscription
	Unregister chan Subscription
}
