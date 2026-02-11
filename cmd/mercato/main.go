package main

import (
	"log"
	"net/http"
	"os"

	"github.com/prathxm/mercato/internal/db"
	"github.com/prathxm/mercato/internal/handlers"
	"github.com/prathxm/mercato/internal/ws"
)

func main() {
	dbPath := "./data/mercato.db"
	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	hub := ws.NewHub()
	go hub.Run()

	h := handlers.NewHandlers(database, hub)

	mux := http.NewServeMux()
	
	// Routes
	mux.HandleFunc("GET /", h.Home)
	mux.HandleFunc("POST /create", h.CreateFamily)
	mux.HandleFunc("GET /list/{token}", h.FamilyView)
	mux.HandleFunc("GET /shop/{token}", h.ShopView)
	mux.HandleFunc("POST /add", h.AddItem)
	mux.HandleFunc("POST /shop/buy/{id}", h.BuyItem)
	mux.HandleFunc("POST /shop/unavailable/{id}", h.MarkUnavailable)
	mux.HandleFunc("POST /shop/complete", h.CompleteList)
	mux.HandleFunc("DELETE /item/{id}", h.DeleteItem)
	mux.HandleFunc("GET /ws/{token}", h.ServeWS)
	mux.HandleFunc("GET /ws/{view}/{token}", h.ServeWS)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	log.Printf("Mercato starting on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
