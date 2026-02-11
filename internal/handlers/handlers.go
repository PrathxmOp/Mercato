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

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateShortToken(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[uuid.New().ID()%uint32(len(charset))]
	}
	return string(b)
}

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

	famToken := generateShortToken(6)
	shopToken := generateShortToken(6)

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

	if family.Status == "done" {
		http.Error(w, "List is complete and read-only", http.StatusForbidden)
		return
	}

	item, err := h.DB.AddItem(family.ID, name, qty)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

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

func (h *Handlers) MarkUnavailable(w http.ResponseWriter, r *http.Request) {
	itemIDStr := r.PathValue("id")
	itemID, _ := strconv.ParseInt(itemIDStr, 10, 64)

	item, err := h.DB.UpdateItemStatus(itemID, 0, "unavailable")
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	family, _ := h.DB.GetFamilyByTokenFromID(item.FamilyID)
	h.broadcastItem(r.Context(), family.Token, *item)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) DeleteItem(w http.ResponseWriter, r *http.Request) {
	itemIDStr := r.PathValue("id")
	itemID, _ := strconv.ParseInt(itemIDStr, 10, 64)

	item, err := h.DB.DeleteItem(itemID)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	family, _ := h.DB.GetFamilyByTokenFromID(item.FamilyID)
	if family.Status == "done" {
		http.Error(w, "List is complete and read-only", http.StatusForbidden)
		return
	}

	h.broadcastDeletion(family.Token, item.ID, family.ID)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) CompleteList(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	family, err := h.DB.GetFamilyByToken(token, true)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	err = h.DB.MarkFamilyDone(family.ID)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	// Broadcast that the list is done to update all clients UI
	payload := `<div id="list-status-badge" hx-swap-oob="innerHTML">
		<span class="inline-flex items-center gap-1.5 bg-indigo-100 text-indigo-700 text-xs font-bold px-3 py-1.5 rounded-xl border border-indigo-200">
			Shopping Complete
		</span>
	</div>
	<div id="add-item-form-container" hx-swap-oob="innerHTML">
		<div class="text-center py-4 bg-slate-50 rounded-2xl border border-dashed border-slate-200">
			<p class="text-slate-500 font-semibold text-sm">Shopping is complete. This list is now read-only.</p>
		</div>
	</div>
	<div id="shop-actions-container" hx-swap-oob="delete"></div>`
	
	h.Hub.Broadcast <- ws.Message{Room: family.Token, Payload: []byte(payload), TargetShop: nil}
	w.WriteHeader(http.StatusOK)
}

// broadcastItem sends the correct ItemRow version to each view type separately.
// Family clients (IsShop=false) get isShop=false rows.
// Shop clients  (IsShop=true)  get isShop=true rows.
// This prevents duplicates caused by both versions landing in the same #grocery-list.
func (h *Handlers) broadcastItem(ctx context.Context, room string, item db.Item) {
	isNew := item.Status == "pending" && item.Price == 0
	family, _ := h.DB.GetFamilyByTokenFromID(item.FamilyID)
	isDone := family != nil && family.Status == "done"

	// --- Payload for family/list view (isShop = false) ---
	var famBuf bytes.Buffer
	if isNew {
		// Prepend new row into the list container
		famBuf.WriteString(`<div id="grocery-list" hx-swap-oob="afterbegin">`)
		components.ItemRow(item, false, isDone).Render(ctx, &famBuf)
		famBuf.WriteString(`</div>`)
		famBuf.WriteString(`<div id="empty-list-placeholder" hx-swap-oob="delete"></div>`)
	} else {
		// Replace the existing row in-place
		famBuf.WriteString(fmt.Sprintf(`<div id="item-%d" hx-swap-oob="outerHTML:#item-%d">`, item.ID, item.ID))
		components.ItemRow(item, false, isDone).Render(ctx, &famBuf)
		famBuf.WriteString(`</div>`)
	}

	// --- Payload for shop view (isShop = true) ---
	var shopBuf bytes.Buffer
	if isNew {
		shopBuf.WriteString(`<div id="grocery-list" hx-swap-oob="afterbegin">`)
		components.ItemRow(item, true, isDone).Render(ctx, &shopBuf)
		shopBuf.WriteString(`</div>`)
		shopBuf.WriteString(`<div id="empty-list-placeholder" hx-swap-oob="delete"></div>`)
	} else {
		shopBuf.WriteString(fmt.Sprintf(`<div id="item-%d" hx-swap-oob="outerHTML:#item-%d">`, item.ID, item.ID))
		components.ItemRow(item, true, isDone).Render(ctx, &shopBuf)
		shopBuf.WriteString(`</div>`)
	}

	// Send each payload only to the matching client type
	isFam := ws.BoolPtr(false)
	isShop := ws.BoolPtr(true)

	h.Hub.Broadcast <- ws.Message{Room: room, Payload: famBuf.Bytes(), TargetShop: isFam}
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: shopBuf.Bytes(), TargetShop: isShop}

	h.broadcastTotalPrice(item.FamilyID, room)
	h.broadcastCounts(item.FamilyID, room)
}

func (h *Handlers) broadcastTotalPrice(familyID int64, room string) {
	items, _ := h.DB.GetItems(familyID)
	var total float64
	for _, i := range items {
		if i.Status == "bought" {
			total += i.Price
		}
	}
	payload := fmt.Sprintf(`<span id="total-price" hx-swap-oob="innerHTML">$%.2f</span>`, total)
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: []byte(payload), TargetShop: nil}
}

func (h *Handlers) broadcastCounts(familyID int64, room string) {
	items, _ := h.DB.GetItems(familyID)
	total := len(items)
	done := 0
	for _, i := range items {
		if i.Status == "bought" {
			done++
		}
	}

	shoppingPayload := fmt.Sprintf(`<p class="text-xl font-black text-slate-700" id="shopping-count" hx-swap-oob="innerHTML">%d</p>`, total)
	donePayload := fmt.Sprintf(`<p class="text-xl font-black text-emerald-600" id="done-count" hx-swap-oob="innerHTML">%d</p>`, done)

	h.Hub.Broadcast <- ws.Message{Room: room, Payload: []byte(shoppingPayload), TargetShop: nil}
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: []byte(donePayload), TargetShop: nil}
}

// broadcastDeletion removes the row from all clients regardless of view type.
func (h *Handlers) broadcastDeletion(room string, itemID int64, familyID int64) {
	payload := fmt.Sprintf(`<div id="item-%d" hx-swap-oob="delete"></div>`, itemID)
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: []byte(payload), TargetShop: nil}

	// Check if we became empty
	items, _ := h.DB.GetItems(familyID)
	if len(items) == 0 {
		var famBuf, shopBuf bytes.Buffer
		famBuf.WriteString(`<div id="grocery-list" hx-swap-oob="beforeend">`)
		components.EmptyState(false).Render(context.Background(), &famBuf)
		famBuf.WriteString(`</div>`)

		shopBuf.WriteString(`<div id="grocery-list" hx-swap-oob="beforeend">`)
		components.EmptyState(true).Render(context.Background(), &shopBuf)
		shopBuf.WriteString(`</div>`)

		isFam := ws.BoolPtr(false)
		isShop := ws.BoolPtr(true)
		h.Hub.Broadcast <- ws.Message{Room: room, Payload: famBuf.Bytes(), TargetShop: isFam}
		h.Hub.Broadcast <- ws.Message{Room: room, Payload: shopBuf.Bytes(), TargetShop: isShop}
	}

	h.broadcastTotalPrice(familyID, room)
	h.broadcastCounts(familyID, room)
}

// ServeWS upgrades the connection and registers the client with the correct view type.
// The WS endpoint must encode whether it's a shop or family connection.
// Route it as: /ws/:token        → family view
//              /ws/shop/:token   → shop view
func (h *Handlers) ServeWS(w http.ResponseWriter, r *http.Request) {
	room := r.PathValue("token")
	isShop := r.PathValue("view") == "shop" // set your router to pass "shop" for the shop route

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	client := &ws.Client{
		Hub:    h.Hub,
		Conn:   conn,
		Send:   make(chan []byte, 256),
		Room:   room,
		IsShop: isShop,
	}
	client.Hub.Register <- client

	go client.WritePump()
	go client.ReadPump()
}
