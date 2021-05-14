package usecases

import (
	"log"

	"github.com/aryuuu/cepex-server/configs"
	"github.com/aryuuu/cepex-server/models/events"
	gameModel "github.com/aryuuu/cepex-server/models/game"
	"github.com/gorilla/websocket"
)

type connection struct {
	ID    string
	Queue chan interface{}
}

type gameUsecase struct {
	Rooms       map[string]map[*websocket.Conn]*connection
	GameRooms   map[string]*gameModel.Room
	SwitchQueue chan events.SocketEvent
}

func NewConnection(ID string) *connection {
	return &connection{
		ID:    ID,
		Queue: make(chan interface{}, 256),
	}
}

func NewGameUsecase() gameModel.GameUsecase {
	return &gameUsecase{
		Rooms:       make(map[string]map[*websocket.Conn]*connection),
		GameRooms:   make(map[string]*gameModel.Room),
		SwitchQueue: make(chan events.SocketEvent, 256),
	}
}

func (u *gameUsecase) Connect(conn *websocket.Conn, roomID string) {
	for {
		var gameRequest events.GameRequest
		err := conn.ReadJSON(&gameRequest)

		if err != nil {
			log.Print(err)
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Print("IsUnexpectedCloseError()", err)
			} else {
				log.Printf("expected close error: %v", err)
				u.kickPlayer(conn, roomID, gameRequest)
			}
			return
		}
		log.Printf("gameRequest: %v", gameRequest)

		switch gameRequest.EventType {
		case "create-room":
			u.createRoom(conn, roomID, gameRequest)
		case "join-room":
			u.joinRoom(conn, roomID, gameRequest)
		case "leave-room":
			u.kickPlayer(conn, roomID, gameRequest)
		case "kick-player":
			u.kickPlayer(conn, roomID, gameRequest)
		case "start-game":
			u.startGame(conn, roomID)
		case "play-card":
			u.playCard(conn, roomID, gameRequest)
		case "chat":
			u.broadcastChat(conn, roomID, gameRequest)
		default:
		}
	}
}

func (u *gameUsecase) createRoom(conn *websocket.Conn, roomID string, gameRequest events.GameRequest) {
	log.Printf("Client trying to create a new room with ID %v", roomID)

	if len(u.Rooms) >= int(configs.Constant.Capacity) {
		message := events.NewCreateRoomResponse(false, roomID, nil, "Server is full")
		u.pushMessage(false, roomID, conn, message)
		return
	}

	_, ok := u.Rooms[roomID]

	if ok {
		message := events.NewCreateRoomResponse(false, roomID, nil, "Room already exists")
		u.pushMessage(false, roomID, conn, message)
	} else {
		player := gameModel.NewPlayer(gameRequest.ClientName, gameRequest.AvatarURL)

		u.createConnectionRoom(roomID, conn)
		u.createGameRoom(roomID, player.PlayerID)
		u.registerPlayer(roomID, conn, player)

		res := events.NewCreateRoomResponse(true, roomID, player, "")
		u.pushMessage(false, roomID, conn, res)
	}
}

func (u *gameUsecase) joinRoom(conn *websocket.Conn, roomID string, gameRequest events.GameRequest) {
	log.Printf("Client trying to join room %v", roomID)

	_, ok := u.Rooms[roomID]

	if ok {
		log.Printf("found room %v", roomID)
		gameRoom := u.GameRooms[roomID]
		player := gameModel.NewPlayer(gameRequest.ClientName, gameRequest.AvatarURL)
		u.registerPlayer(roomID, conn, player)

		res := events.NewJoinRoomResponse(ok, gameRoom, "")
		u.pushMessage(false, roomID, conn, res)
		// conn.WriteJSON(res)

		broadcast := events.NewJoinRoomBroadcast(player)
		// u.broadcastMessage(roomID, broadcast)
		u.pushMessage(true, roomID, nil, broadcast)
	} else {
		log.Printf("room %v does not exist", roomID)
		res := events.NewJoinRoomResponse(ok, &gameModel.Room{}, "")
		u.pushMessage(false, roomID, conn, res)
		// conn.WriteJSON(res)
	}
}

func (u *gameUsecase) kickPlayer(conn *websocket.Conn, roomID string, gameRequest events.GameRequest) {
	log.Printf("Client trying to leave room %v", roomID)

	var playerID string

	if gameRequest.PlayerID == "" {
		playerID = u.Rooms[roomID][conn].ID
	} else {
		playerID = gameRequest.PlayerID
	}

	_, ok := u.Rooms[roomID]
	res := events.NewLeaveRoomResponse(true)
	// conn.WriteJSON(res)
	u.pushMessage(false, roomID, conn, res)

	if ok {
		broadcast := events.NewLeaveRoomBroadcast(playerID)
		u.pushMessage(true, roomID, conn, broadcast)
		// u.broadcastMessage(roomID, broadcast)
	}

	// appoint new host if necessary
	if u.GameRooms[roomID].HostID == playerID {
		newHostID := u.GameRooms[roomID].NextHost()
		changeHostBroadcast := events.NewChangeHostBroadcast(newHostID)
		u.pushMessage(true, roomID, conn, changeHostBroadcast)
	}

	// u.unregisterPlayer(roomID, conn, playerID)
}

func (u *gameUsecase) startGame(conn *websocket.Conn, roomID string) {
	log.Printf("Client trying to start game on room %v", roomID)
	gameRoom := u.GameRooms[roomID]
	playerID := u.Rooms[roomID][conn].ID

	if playerID != gameRoom.HostID {
		res := events.NewStartGameResponse(false)
		u.pushMessage(false, roomID, conn, res)
		// conn.WriteJSON(res)

	} else {
		starterIndex := gameRoom.StartGame()

		u.dealCard(roomID)

		notifContent := "game started, " + gameRoom.Players[starterIndex].Name + "'s turn"
		notification := events.NewNotificationBroadcast(notifContent)
		res := events.NewStartGameBroadcast(starterIndex)

		u.pushMessage(true, roomID, conn, res)
		u.pushMessage(true, roomID, conn, notification)
		// u.broadcastMessage(roomID, res)
		// u.broadcastMessage(roomID, notification)
	}
}

func (u *gameUsecase) playCard(conn *websocket.Conn, roomID string, gameRequest events.GameRequest) {
	gameRoom := u.GameRooms[roomID]
	playerID := u.Rooms[roomID][conn].ID
	if !gameRoom.IsStarted {
		log.Printf("game is not started")
		res := events.NewPlayCardResponse(false, nil, "Game is not started")
		u.pushMessage(false, roomID, conn, res)
		return
	}

	if gameRoom.TurnID != playerID {
		log.Printf("its not your turn yet")
		res := events.NewPlayCardResponse(false, nil, "Please wait for your turn")
		u.pushMessage(false, roomID, conn, res)
		return
	}

	playerIndex := gameRoom.GetPlayerIndex(playerID)

	player := gameRoom.PlayerMap[playerID]

	if !player.IsAlive {
		log.Printf("this player is dead")
		res := events.NewPlayCardResponse(false, nil, "You are already dead")
		u.pushMessage(false, roomID, conn, res)
		return
	}

	for _, p := range gameRoom.Players {
		log.Printf("%v's card %v", p.Name, p.Hand)
	}

	playedCard := player.Hand[gameRequest.HandIndex]
	log.Printf("%v is playing: %v", player.Name, playedCard)

	var res events.PlayCardResponse

	success := true
	if err := gameRoom.PlayCard(playerID, gameRequest.HandIndex, gameRequest.IsAdd, gameRequest.PlayerID); err != nil {
		success = false
	}

	if len(player.Hand) == 0 {
		player.IsAlive = false
		deadBroadcast := events.NewDeadPlayerBroadcast(player.PlayerID)
		u.pushMessage(true, roomID, conn, deadBroadcast)
	}

	for _, p := range gameRoom.Players {
		log.Printf("%v's card %v", p.Name, p.Hand)
	}

	if winner := gameRoom.GetWinner(); winner != "" {
		// gameRoom.IsStarted = false
		gameRoom.EndGame()
		endBroadcast := events.NewEndGameBroadcast(winner)
		u.pushMessage(true, roomID, conn, endBroadcast)
	}

	message := ""
	if !success {
		message = "Hand discarded"
	}
	res = events.NewPlayCardResponse(success, player.Hand, message)
	u.pushMessage(false, roomID, conn, res)

	var nextPlayerIndex int
	if gameRoom.TurnID == playerID {
		nextPlayerIndex = gameRoom.NextPlayer(playerIndex)
	} else {
		nextPlayerIndex = gameRoom.GetPlayerIndex(gameRoom.TurnID)
	}

	broadcast := events.NewPlayCardBroadcast(playedCard, gameRoom.Count, gameRoom.IsClockwise, nextPlayerIndex)
	u.pushMessage(true, roomID, conn, broadcast)
}

func (u *gameUsecase) broadcastChat(conn *websocket.Conn, roomID string, gameRequest events.GameRequest) {
	log.Printf("Client is sending chat on room %v", roomID)

	room, ok := u.Rooms[roomID]
	if ok {
		playerID := room[conn].ID
		playerName := u.GameRooms[roomID].PlayerMap[playerID].Name

		log.Printf("player %s send chat", playerName)
		broadcast := events.NewMessageBroadcast(gameRequest.Message, playerName)
		// u.broadcastMessage(roomID, broadcast)
		u.pushMessage(true, roomID, conn, broadcast)
	}
}

func (u *gameUsecase) createConnectionRoom(roomID string, conn *websocket.Conn) {
	u.Rooms[roomID] = make(map[*websocket.Conn]*connection)
}

func (u *gameUsecase) createGameRoom(roomID string, hostID string) {
	gameRoom := gameModel.NewRoom(roomID, hostID, 4)
	u.GameRooms[roomID] = gameRoom
}

func (u *gameUsecase) registerPlayer(roomID string, conn *websocket.Conn, player *gameModel.Player) {
	// u.Rooms[roomID][conn] = player.PlayerID
	u.Rooms[roomID][conn] = NewConnection(player.PlayerID)
	u.GameRooms[roomID].AddPlayer(player)
	go u.writePump(conn, roomID)
}

func (u *gameUsecase) unregisterPlayer(roomID string, conn *websocket.Conn, playerID string) {
	playerIndex := -1
	for i, p := range u.GameRooms[roomID].Players {
		if p.PlayerID == playerID {
			playerIndex = i
			break
		}
	}

	gameRoom := u.GameRooms[roomID]
	gameRoom.RemovePlayer(playerIndex)
	delete(u.Rooms[roomID], conn)

	// delete empty room
	if len(u.GameRooms[roomID].Players) == 0 {
		log.Printf("delete room %v", roomID)
		delete(u.GameRooms, roomID)
		delete(u.Rooms, roomID)
	}
}

// func (u *gameUsecase) broadcastMessage(roomID string, message interface{}) {
// 	room := u.Rooms[roomID]
// 	for connection := range room {
// 		connection.WriteJSON(message)
// 	}
// }
func (u *gameUsecase) writePump(conn *websocket.Conn, roomID string) {
	c := u.Rooms[roomID][conn]

	defer func() {
		conn.Close()
	}()

	for {
		message := <-c.Queue
		// log.Println(message, "is about to be delivered")
		conn.WriteJSON(message)

		if _, ok := message.(events.LeaveRoomResponse); ok {
			u.unregisterPlayer(roomID, conn, c.ID)
		}
	}
}

func (u *gameUsecase) dealCard(roomID string) {
	room := u.Rooms[roomID]

	for connection, playerID := range room {
		player := u.GameRooms[roomID].PlayerMap[playerID.ID]
		message := events.NewInitialHandResponse(player.Hand)
		// connection.WriteJSON(message)
		u.pushMessage(false, roomID, connection, message)
	}
}

func (u *gameUsecase) RunSwitch() {
	for {
		event := <-u.SwitchQueue
		if event.EventType == "unicast" {
			u.Rooms[event.RoomID][event.Conn].Queue <- event.Message
		} else {
			for _, con := range u.Rooms[event.RoomID] {
				con.Queue <- event.Message

			}
		}
	}
}

func (u *gameUsecase) pushMessage(broadcast bool, roomID string, conn *websocket.Conn, message interface{}) {
	// log.Printf("pushing message to room %v", roomID)
	if broadcast {
		event := events.NewBroadcastEvent(roomID, message)
		// log.Printf("push message %#v", event)
		u.SwitchQueue <- event
		// log.Println("broadcast event pushed")
	} else {
		event := events.NewUnicastEvent(roomID, conn, message)
		// log.Printf("push message %#v", event)
		u.SwitchQueue <- event
		// log.Println("unicast event pushed")
	}
}

// func (u *gameUsecase) SendMessage(connID string, message interface{}) {

// }

// func (u *gameUsecase) BroadcastMessage(roomID string, message interface{}) {

// }
