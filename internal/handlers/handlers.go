package handlers

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"


	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/prathxm/mercato/internal/db"
	"github.com/prathxm/mercato/internal/ws"
	"github.com/prathxm/mercato/view/components"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Handlers struct {
	DB  *db.DB
	Hub *ws.Hub
}

func NewHandlers(database *db.DB, hub *ws.Hub) *Handlers {
	return &Handlers{DB: database, Hub: hub}
}

func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	components.Home().Render(r.Context(), w)
}

func (h *Handlers) CreateFamily(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	famToken := "fam_" + uuid.NewString()
	shopToken := "shop_" + uuid.NewString()

	err := h.DB.CreateFamily(name, famToken, shopToken)
	if err != nil {
		log.Printf("Error creating family: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/list/"+famToken, http.StatusSeeOther)
}

func (h *Handlers) FamilyView(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	family, err := h.DB.GetFamilyByToken(token, false)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	items, _ := h.DB.GetItems(family.ID)
	components.FamilyView(family, items).Render(r.Context(), w)
}

func (h *Handlers) ShopView(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	family, err := h.DB.GetFamilyByToken(token, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	items, _ := h.DB.GetItems(family.ID)
	components.ShopView(family, items).Render(r.Context(), w)
}

func (h *Handlers) AddItem(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	name := r.FormValue("name")
	qty := r.FormValue("quantity")

	family, err := h.DB.GetFamilyByToken(token, false)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	item, err := h.DB.AddItem(family.ID, name, qty)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	// Broadcast the new item to all clients in the family room
	h.broadcastItem(r.Context(), family.Token, *item)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) BuyItem(w http.ResponseWriter, r *http.Request) {
	itemIDStr := r.PathValue("id")
	itemID, _ := strconv.ParseInt(itemIDStr, 10, 64)
	priceStr := r.FormValue("price")
	price, _ := strconv.ParseFloat(priceStr, 64)

	item, err := h.DB.UpdateItemStatus(itemID, price, "bought")
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	family, _ := h.DB.GetFamilyByTokenFromID(item.FamilyID)
	h.broadcastItem(r.Context(), family.Token, *item)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) broadcastItem(ctx context.Context, room string, item db.Item) {
	var buf bytes.Buffer
	// Wrap in a div that tells HTMX where to swap
	buf.WriteString(fmt.Sprintf("<div id=\"grocery-list\" hx-swap-oob=\"afterbegin\">"))
	components.ItemRow(item, false).Render(ctx, &buf)
	buf.WriteString("</div>")

	// We also need a version for the shop view if they are both open
	// For simplicity in this iteration, we broadcast a message that works for both or updates the specific ID
	var buf2 bytes.Buffer
	buf2.WriteString(fmt.Sprintf("<div id=\"item-%d\" hx-swap-oob=\"outerHTML\">", item.ID))
	components.ItemRow(item, false).Render(ctx, &buf2) // Family view version
	buf2.WriteString("</div>")
	
	// Actually, let's just broadcast the updated row based on ID for sync
	var syncBuf bytes.Buffer
	// For family view
	syncBuf.WriteString(fmt.Sprintf("<div id=\"item-%d\" hx-swap-oob=\"outerHTML\">", item.ID))
	components.ItemRow(item, false).Render(ctx, &syncBuf)
	syncBuf.WriteString("</div>")
	// For shop view
	syncBuf.WriteString(fmt.Sprintf("<div id=\"item-%d\" hx-swap-oob=\"outerHTML\">", item.ID))
	components.ItemRow(item, true).Render(ctx, &syncBuf)
	syncBuf.WriteString("</div>")
	
	// If it's a new item, we use afterbegin on the list
	if item.Status == "pending" && item.Price == 0 {
		var newBuf bytes.Buffer
		newBuf.WriteString("<div id=\"grocery-list\" hx-swap-oob=\"afterbegin\">")
		components.ItemRow(item, false).Render(ctx, &newBuf)
		newBuf.WriteString("</div>")
		newBuf.WriteString("<div id=\"grocery-list\" hx-swap-oob=\"afterbegin\">")
		components.ItemRow(item, true).Render(ctx, &newBuf)
		newBuf.WriteString("</div>")
		h.Hub.Broadcast <- ws.Message{Room: room, Payload: newBuf.Bytes()}
	} else {
		h.Hub.Broadcast <- ws.Message{Room: room, Payload: syncBuf.Bytes()}
	}
}

func (h *Handlers) ServeWS(w http.ResponseWriter, r *http.Request) {
	room := r.PathValue("token")
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	client := &ws.Client{
		Hub:  h.Hub,
		Conn: conn,
		Send: make(chan []byte, 256),
		Room: room,
	}
	client.Hub.Register <- client

	go client.WritePump()
	go client.ReadPump()
}
