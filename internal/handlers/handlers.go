package handlers

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/prathxm/mercato/internal/db"
	"github.com/prathxm/mercato/internal/i18n"
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

func InitI18n(dir string) error {
	return i18n.Init(dir)
}

func GetLang(ctx context.Context) string {
	return i18n.GetLang(ctx)
}

func (h *Handlers) LangMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := "auto"
		// 1. Check for language cookie
		if cookie, err := r.Cookie("lang"); err == nil {
			lang = cookie.Value
		}

		finalLang := lang
		if lang == "auto" || lang == "" {
			// 2. Fallback to Accept-Language header
			acceptLang := r.Header.Get("Accept-Language")
			if acceptLang != "" {
				parts := strings.Split(acceptLang, ",")
				if len(parts) > 0 {
					primary := strings.Split(parts[0], ";")[0]
					primary = strings.Split(primary, "-")[0]
					finalLang = primary
				}
			} else {
				finalLang = "en" // Ultimate fallback
			}
		}

		ctx := i18n.ContextWithLang(r.Context(), finalLang)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		h.NotFound(w, r)
		return
	}
	components.Home().Render(r.Context(), w)
}

func (h *Handlers) NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	components.NotFound().Render(r.Context(), w)
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
		h.NotFound(w, r)
		return
	}

	items, _ := h.DB.GetItems(family.ID)
	messages, _ := h.DB.GetMessages(family.ID)
	unreadCount, _ := h.DB.GetUnreadCount(family.ID, "family")
	components.FamilyView(family, items, messages, unreadCount).Render(r.Context(), w)
}

func (h *Handlers) ShopView(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	family, err := h.DB.GetFamilyByToken(token, true)
	if err != nil {
		h.NotFound(w, r)
		return
	}

	items, _ := h.DB.GetItems(family.ID)
	messages, _ := h.DB.GetMessages(family.ID)
	unreadCount, _ := h.DB.GetUnreadCount(family.ID, "shop")
	components.ShopView(family, items, messages, unreadCount).Render(r.Context(), w)
}

func (h *Handlers) AddItem(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	name := r.FormValue("name")
	qty := r.FormValue("quantity")
	category := r.FormValue("category")

	family, err := h.DB.GetFamilyByToken(token, false)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if family.Status == "done" {
		http.Error(w, "List is complete and read-only", http.StatusForbidden)
		return
	}

	item, err := h.DB.AddItem(family.ID, name, qty, category)
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
	lang := GetLang(r.Context())
	payload := fmt.Sprintf(`<div id="list-status-badge" hx-swap-oob="innerHTML">
		<span class="inline-flex items-center gap-1.5 bg-blue-100 text-blue-700 text-xs font-bold px-3 py-1.5 rounded-xl border border-blue-200">
			%s
		</span>
	</div>
	<div id="add-item-form-container" hx-swap-oob="innerHTML">
		<div class="text-center py-4 bg-slate-50 rounded-2xl border border-dashed border-slate-200">
			<p class="text-slate-500 dark:text-slate-400 font-semibold text-sm">%s</p>
		</div>
	</div>
	<div id="shop-actions-container" hx-swap-oob="delete"></div>`, 
	i18n.T(lang, "list.complete"), 
	i18n.T(lang, "list.read_only"))
	
	h.Hub.Broadcast <- ws.Message{Room: family.Token, Payload: []byte(payload), TargetShop: nil}
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) SetCurrency(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	currency := r.FormValue("currency")
	if currency == "" {
		http.Error(w, "Currency is required", http.StatusBadRequest)
		return
	}

	family, err := h.DB.GetFamilyByToken(token, false)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	err = h.DB.SetCurrency(family.ID, currency)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	// Refresh total price and counts as well
	h.broadcastTotalPrice(family.ID, family.Token)
	h.broadcastCounts(family.ID, family.Token)
}

func (h *Handlers) SetFamilySettings(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	expiryStr := r.FormValue("expiry")
	lang := r.FormValue("lang")

	expiry, _ := strconv.Atoi(expiryStr)

	family, err := h.DB.GetFamilyByToken(token, false)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	err = h.DB.UpdateFamilySettings(family.ID, expiry, lang)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	// Set language cookie if manually selected
	http.SetCookie(w, &http.Cookie{
		Name:     "lang",
		Value:    lang,
		Path:     "/",
		MaxAge:   31536000, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// Full refresh to update UI with new language/settings
	h.broadcastList(r.Context(), family)
	
	// If it's an HTMX request, we might want to trigger a page refresh or just let OOB handles it
	// For language change, a full page refresh is often cleaner to update all localized strings
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) broadcastList(ctx context.Context, family *db.Family) {
	items, _ := h.DB.GetItems(family.ID)
	isDone := family.Status == "done"

	// Family view
	var famBuf bytes.Buffer
	famBuf.WriteString(`<div id="grocery-list" hx-swap-oob="outerHTML">`)
	components.ItemList(items, false, isDone, family.Currency).Render(ctx, &famBuf)
	famBuf.WriteString(`</div>`)

	// Shop view
	var shopBuf bytes.Buffer
	shopBuf.WriteString(`<div id="grocery-list" hx-swap-oob="outerHTML">`)
	components.ItemList(items, true, isDone, family.Currency).Render(ctx, &shopBuf)
	shopBuf.WriteString(`</div>`)

	// Send to clients
	isFam := ws.BoolPtr(false)
	isShop := ws.BoolPtr(true)
	h.Hub.Broadcast <- ws.Message{Room: family.Token, Payload: famBuf.Bytes(), TargetShop: isFam}
	h.Hub.Broadcast <- ws.Message{Room: family.Token, Payload: shopBuf.Bytes(), TargetShop: isShop}

	// Refresh total price and counts as well
	h.broadcastTotalPrice(family.ID, family.Token)
	h.broadcastCounts(family.ID, family.Token)
}

func (h *Handlers) SendChatMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.PathValue("token")
	isShop := r.PathValue("view") == "shop"

	family, err := h.DB.GetFamilyByToken(token, isShop)
	if err != nil {
		http.Error(w, "Family not found", http.StatusNotFound)
		return
	}

	content := r.FormValue("content")
	if content == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	sender := "family"
	if isShop {
		sender = "shop"
	}

	msg, err := h.DB.CreateMessage(family.ID, sender, content)
	if err != nil {
		http.Error(w, "Failed to save message", http.StatusInternalServerError)
		return
	}

	h.broadcastMessage(ctx, family.Token, msg)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) MarkChatRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.PathValue("token")
	view := r.PathValue("view")
	isShop := view == "shop"

	family, err := h.DB.GetFamilyByToken(token, isShop)
	if err != nil {
		log.Printf("MarkChatRead: Family not found for token %s, view %s", token, view)
		http.Error(w, "Family not found", http.StatusNotFound)
		return
	}

	viewer := "family"
	if isShop {
		viewer = "shop"
	}

	err = h.DB.MarkMessagesAsRead(family.ID, viewer)
	if err != nil {
		log.Printf("MarkChatRead: Failed to mark messages as read: %v", err)
		http.Error(w, "Failed to mark as read", http.StatusInternalServerError)
		return
	}

	log.Printf("MarkChatRead: %s marked messages as read for family %d", viewer, family.ID)

	// Broadcast updated bell for the viewer
	var bellBuf bytes.Buffer
	components.NotificationBell(0).Render(ctx, &bellBuf)
	bellPayload := fmt.Sprintf(`<div id="chat-notification-bell" hx-swap-oob="outerHTML">%s</div>`, bellBuf.String())
	
	isShopPtr := ws.BoolPtr(isShop)
	h.Hub.Broadcast <- ws.Message{Room: family.Token, Payload: []byte(bellPayload), TargetShop: isShopPtr}

	// ALSO broadcast a refresh to the OTHER party so they see the "Seen" status on their messages
	messages, _ := h.DB.GetMessages(family.ID)
	var otherChatBuf bytes.Buffer
	otherChatBuf.WriteString(`<div id="chat-messages" hx-swap-oob="innerHTML">`)
	components.MessagesList(messages, !isShop).Render(ctx, &otherChatBuf)
	otherChatBuf.WriteString(`<div id="chat-bottom"></div></div>`)
	
	isOtherShopPtr := ws.BoolPtr(!isShop)
	h.Hub.Broadcast <- ws.Message{Room: family.Token, Payload: otherChatBuf.Bytes(), TargetShop: isOtherShopPtr}
 
	w.WriteHeader(http.StatusOK)
}

// broadcastItem sends the correct ItemRow version to each view type separately.
// Family clients (IsShop=false) get isShop=false rows.
// Shop clients  (IsShop=true)  get isShop=true rows.
// This prevents duplicates caused by both versions landing in the same #grocery-list.
func (h *Handlers) broadcastItem(ctx context.Context, room string, item db.Item) {
	items, _ := h.DB.GetItems(item.FamilyID)
	family, _ := h.DB.GetFamilyByTokenFromID(item.FamilyID)
	isDone := family != nil && family.Status == "done"

	currency := "₹"
	if family != nil {
		currency = family.Currency
	}

	// --- Payload for family/list view (isShop = false) ---
	var famBuf bytes.Buffer
	famBuf.WriteString(`<div id="grocery-list" hx-swap-oob="outerHTML">`)
	components.ItemList(items, false, isDone, currency).Render(ctx, &famBuf)
	famBuf.WriteString(`</div>`)

	// --- Payload for shop view (isShop = true) ---
	var shopBuf bytes.Buffer
	shopBuf.WriteString(`<div id="grocery-list" hx-swap-oob="outerHTML">`)
	components.ItemList(items, true, isDone, currency).Render(ctx, &shopBuf)
	shopBuf.WriteString(`</div>`)

	// Send each payload only to the matching client type
	isFam := ws.BoolPtr(false)
	isShop := ws.BoolPtr(true)

	h.Hub.Broadcast <- ws.Message{Room: room, Payload: famBuf.Bytes(), TargetShop: isFam}
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: shopBuf.Bytes(), TargetShop: isShop}

	h.broadcastTotalPrice(item.FamilyID, room)
	h.broadcastCounts(item.FamilyID, room)
}

func (h *Handlers) broadcastTotalPrice(familyID int64, room string) {
	family, _ := h.DB.GetFamilyByTokenFromID(familyID)
	currency := "₹"
	if family != nil {
		currency = family.Currency
	}

	items, _ := h.DB.GetItems(familyID)
	var total float64
	for _, i := range items {
		if i.Status == "bought" {
			total += i.Price
		}
	}
	payload := fmt.Sprintf(`
		<span id="total-price" hx-swap-oob="innerHTML">%s%.2f</span>
		<span id="total-price-mobile" hx-swap-oob="innerHTML">%.2f</span>`, 
		currency, total, total)
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

	shoppingPayload := fmt.Sprintf(`<p class="text-xl font-black text-slate-900 dark:text-white tabular-nums" id="shopping-count" hx-swap-oob="innerHTML">%d</p>`, total)
	donePayload := fmt.Sprintf(`<p class="text-xl font-black text-emerald-600 dark:text-emerald-500 tabular-nums" id="done-count" hx-swap-oob="innerHTML">%d</p>`, done)

	h.Hub.Broadcast <- ws.Message{Room: room, Payload: []byte(shoppingPayload), TargetShop: nil}
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: []byte(donePayload), TargetShop: nil}
}

// broadcastDeletion removes the row from all clients regardless of view type.
func (h *Handlers) broadcastDeletion(room string, itemID int64, familyID int64) {
	items, _ := h.DB.GetItems(familyID)
	family, _ := h.DB.GetFamilyByTokenFromID(familyID)
	isDone := family != nil && family.Status == "done"

	currency := "₹"
	if family != nil {
		currency = family.Currency
	}

	// --- Payload for family view ---
	var famBuf bytes.Buffer
	famBuf.WriteString(`<div id="grocery-list" hx-swap-oob="outerHTML">`)
	components.ItemList(items, false, isDone, currency).Render(context.Background(), &famBuf)
	famBuf.WriteString(`</div>`)

	// --- Payload for shop view ---
	var shopBuf bytes.Buffer
	shopBuf.WriteString(`<div id="grocery-list" hx-swap-oob="outerHTML">`)
	components.ItemList(items, true, isDone, currency).Render(context.Background(), &shopBuf)
	shopBuf.WriteString(`</div>`)

	isFam := ws.BoolPtr(false)
	isShop := ws.BoolPtr(true)

	h.Hub.Broadcast <- ws.Message{Room: room, Payload: famBuf.Bytes(), TargetShop: isFam}
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: shopBuf.Bytes(), TargetShop: isShop}

	h.broadcastTotalPrice(familyID, room)
	h.broadcastCounts(familyID, room)
}

func (h *Handlers) broadcastMessage(ctx context.Context, room string, msg db.Message) {
	messages, _ := h.DB.GetMessages(msg.FamilyID)

	// --- Payload for family view ---
	var famMsgBuf bytes.Buffer
	famMsgBuf.WriteString(`<div id="chat-messages" hx-swap-oob="innerHTML">`)
	components.MessagesList(messages, false).Render(ctx, &famMsgBuf)
	famMsgBuf.WriteString(`<div id="chat-bottom"></div><script>document.getElementById('chat-bottom').scrollIntoView({behavior: 'smooth'})</script></div>`)

	// --- Payload for shop view ---
	var shopMsgBuf bytes.Buffer
	shopMsgBuf.WriteString(`<div id="chat-messages" hx-swap-oob="innerHTML">`)
	components.MessagesList(messages, true).Render(ctx, &shopMsgBuf)
	shopMsgBuf.WriteString(`<div id="chat-bottom"></div><script>document.getElementById('chat-bottom').scrollIntoView({behavior: 'smooth'})</script></div>`)

	// --- Notification Bell status ---
	// For family (if shop sent msg)
	unreadForFam, _ := h.DB.GetUnreadCount(msg.FamilyID, "family")
	var famBellBuf bytes.Buffer
	components.NotificationBell(unreadForFam).Render(ctx, &famBellBuf)
	famBellPayload := fmt.Sprintf(`<div id="chat-notification-bell" hx-swap-oob="outerHTML">%s</div>`, famBellBuf.String())

	// For shop (if family sent msg)
	unreadForShop, _ := h.DB.GetUnreadCount(msg.FamilyID, "shop")
	var shopBellBuf bytes.Buffer
	components.NotificationBell(unreadForShop).Render(ctx, &shopBellBuf)
	shopBellPayload := fmt.Sprintf(`<div id="chat-notification-bell" hx-swap-oob="outerHTML">%s</div>`, shopBellBuf.String())

	isFam := ws.BoolPtr(false)
	isShop := ws.BoolPtr(true)

	// Send message to relevant chat windows
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: famMsgBuf.Bytes(), TargetShop: isFam}
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: shopMsgBuf.Bytes(), TargetShop: isShop}

	// Send notification bell updates
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: []byte(famBellPayload), TargetShop: isFam}
	h.Hub.Broadcast <- ws.Message{Room: room, Payload: []byte(shopBellPayload), TargetShop: isShop}
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
