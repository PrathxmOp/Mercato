package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prathxm/mercato/internal/db"
	"github.com/prathxm/mercato/internal/handlers"
	"github.com/prathxm/mercato/internal/ws"
	"github.com/prathxm/mercato/locales"
)

func main() {
	// Admin Flags
	delAll := flag.Bool("delall", false, "Delete all data and empty database")
	showStats := flag.Bool("stats", false, "Show database statistics")
	listFamilies := flag.Bool("list", false, "List all active families")
	flag.Parse()

	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./data/mercato.db"
	}
	
	database, err := db.InitDB(dbPath)
	if err != nil {
		slog.Error("Failed to initialize database", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	// Handle Admin Commands
	if *delAll {
		slog.Info("Deleting all records as requested by --delall flag...")
		if err := database.DeleteAll(); err != nil {
			slog.Error("Failed to delete all data", "err", err)
			os.Exit(1)
		}
		slog.Info("Database cleared successfully.")
		os.Exit(0)
	}

	if *showStats {
		f, i, m, err := database.GetStats()
		if err != nil {
			slog.Error("Failed to get database stats", "err", err)
			os.Exit(1)
		}
		fmt.Printf("Mercato Database Statistics:\n")
		fmt.Printf("- Families: %d\n", f)
		fmt.Printf("- Items:    %d\n", i)
		fmt.Printf("- Messages: %d\n", m)
		os.Exit(0)
	}

	if *listFamilies {
		families, err := database.ListFamilies()
		if err != nil {
			slog.Error("Failed to list families", "err", err)
			os.Exit(1)
		}
		fmt.Printf("Active Families (%d):\n", len(families))
		for _, f := range families {
			fmt.Printf("- [%s] %s (Token: %s)\n", f.Status, f.Name, f.Token)
		}
		os.Exit(0)
	}

	hub := ws.NewHub()
	go hub.Run()

	// Start background cleanup worker for expired lists
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				count, err := database.CleanupExpiredFamilies()
				if err != nil {
					slog.Error("Failed to cleanup expired families", "err", err)
				} else if count > 0 {
					slog.Info("Cleaned up expired families", "count", count)
				}
			}
		}
	}()

	h := handlers.NewHandlers(database, hub)
	
	// Initialize i18n
	localesPath := os.Getenv("LOCALES_PATH")
	var i18nFS fs.FS

	if localesPath != "" {
		i18nFS = os.DirFS(localesPath)
	} else {
		// Try ./locales first (for dev)
		if _, err := os.Stat("./locales"); err == nil {
			i18nFS = os.DirFS("./locales")
		} else {
			// Fallback to embedded locales
			i18nFS = locales.FS
			slog.Info("Using embedded locales")
		}
	}

	if err := handlers.InitI18n(i18nFS); err != nil {
		slog.Error("Failed to initialize i18n", "err", err)
	}

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
	mux.HandleFunc("POST /chat/{view}/{token}/send", h.SendChatMessage)
	mux.HandleFunc("GET /chat/{view}/{token}/read", h.MarkChatRead)
	mux.HandleFunc("DELETE /item/{id}", h.DeleteItem)
	mux.HandleFunc("POST /settings/currency", h.SetCurrency)
	mux.HandleFunc("POST /settings/family", h.SetFamilySettings)
	mux.HandleFunc("GET /ws/{token}", h.ServeWS)
	mux.HandleFunc("GET /ws/{view}/{token}", h.ServeWS)

	// Wrap mux with middleware
	handler := h.LangMiddleware(mux)
	handler = securityHeaders(handler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	// Graceful shutdown setup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("Mercato starting", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "err", err)
			os.Exit(1)
		}
	}()

	<-stop
	slog.Info("Shutting down Mercato...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "err", err)
	}

	slog.Info("Mercato stopped")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://unpkg.com https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdn.jsdelivr.net; font-src https://fonts.gstatic.com; connect-src 'self' ws: wss:")
		next.ServeHTTP(w, r)
	})
}
