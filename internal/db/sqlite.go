package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

type Family struct {
	ID          int64
	Token       string
	Name        string
	ShopToken   string
	Status      string // 'active', 'done'
	Currency    string // e.g., '₹', '$'
	ExpiryHours int    // hours until deletion
	Language    string // 'auto' or specific lang code
}

type Message struct {
	ID        int64
	FamilyID  int64
	Sender    string // 'family', 'shop'
	Content   string
	IsRead    bool
	CreatedAt string
}

type Item struct {
	ID       int64
	FamilyID int64
	Name     string
	Quantity string
	Category string
	Price    float64
	Status   string // 'pending', 'bought'
}

func InitDB(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create data directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping sqlite db: %w", err)
	}

	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	return &DB{db}, nil
}

func createTables(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS families (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		token TEXT UNIQUE NOT NULL,
		name TEXT NOT NULL,
		status TEXT DEFAULT 'active',
		currency TEXT DEFAULT '₹',
		expiry_hours INTEGER DEFAULT 72,
		language TEXT DEFAULT 'auto',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS shops (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		family_id INTEGER NOT NULL,
		token TEXT UNIQUE NOT NULL,
		name TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (family_id) REFERENCES families(id)
	);

	CREATE TABLE IF NOT EXISTS items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		family_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		quantity TEXT,
		category TEXT DEFAULT '',
		price REAL DEFAULT 0,
		status TEXT DEFAULT 'pending',
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (family_id) REFERENCES families(id)
	);

	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		family_id INTEGER NOT NULL,
		sender TEXT NOT NULL, -- 'family', 'shop'
		content TEXT NOT NULL,
		is_read BOOLEAN DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(family_id) REFERENCES families(id)
	);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// Migration: Add status column to families if it doesn't exist (fails silently if already there)
	_, _ = db.Exec("ALTER TABLE families ADD COLUMN status TEXT DEFAULT 'active'")
	_, _ = db.Exec("ALTER TABLE families ADD COLUMN currency TEXT DEFAULT '₹'")
	_, _ = db.Exec("ALTER TABLE families ADD COLUMN expiry_hours INTEGER DEFAULT 72")
	_, _ = db.Exec("ALTER TABLE families ADD COLUMN language TEXT DEFAULT 'auto'")

	return nil
}

// CreateFamily creates a new family and its corresponding shop token
func (d *DB) CreateFamily(name, familyToken, shopToken string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec("INSERT INTO families (token, name) VALUES (?, ?)", familyToken, name)
	if err != nil {
		return err
	}

	familyID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	_, err = tx.Exec("INSERT INTO shops (family_id, token, name) VALUES (?, ?, ?)", familyID, shopToken, name+" Shop")
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetFamilyByToken retrieves family details by either family token or shop token
func (d *DB) GetFamilyByToken(token string, isShop bool) (*Family, error) {
	var f Family
	var query string
	
	if isShop {
		// If using shop token, join tables to get family ID
		query = `SELECT f.id, f.token, f.name, s.token, f.status, f.currency, f.expiry_hours, f.language 
		         FROM families f 
		         JOIN shops s ON f.id = s.family_id 
		         WHERE s.token = ?`
	} else {
		// If using family token, we need to fetch the shop token as well
		query = `SELECT f.id, f.token, f.name, s.token, f.status, f.currency, f.expiry_hours, f.language 
		         FROM families f 
		         JOIN shops s ON f.id = s.family_id 
		         WHERE f.token = ?`
	}

	err := d.QueryRow(query, token).Scan(&f.ID, &f.Token, &f.Name, &f.ShopToken, &f.Status, &f.Currency, &f.ExpiryHours, &f.Language)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (d *DB) GetFamilyByTokenFromID(familyID int64) (*Family, error) {
	var f Family
	query := `SELECT f.id, f.token, f.name, s.token, f.status, f.currency, f.expiry_hours, f.language 
	          FROM families f 
	          JOIN shops s ON f.id = s.family_id 
	          WHERE f.id = ?`
	err := d.QueryRow(query, familyID).Scan(&f.ID, &f.Token, &f.Name, &f.ShopToken, &f.Status, &f.Currency, &f.ExpiryHours, &f.Language)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// AddItem adds a new item to the list
func (d *DB) AddItem(familyID int64, name, quantity, category string) (*Item, error) {
	res, err := d.Exec("INSERT INTO items (family_id, name, quantity, category, status) VALUES (?, ?, ?, ?, 'pending')", familyID, name, quantity, category)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Item{
		ID:       id,
		FamilyID: familyID,
		Name:     name,
		Quantity: quantity,
		Category: category,
		Status:   "pending",
	}, nil
}

// GetItems retrieves all items for a family
func (d *DB) GetItems(familyID int64) ([]Item, error) {
	rows, err := d.Query("SELECT id, family_id, name, quantity, category, price, status FROM items WHERE family_id = ? ORDER BY id DESC", familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var i Item
		if err := rows.Scan(&i.ID, &i.FamilyID, &i.Name, &i.Quantity, &i.Category, &i.Price, &i.Status); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, nil
}

// UpdateItemStatus updates an item's status and price (used by shop)
func (d *DB) UpdateItemStatus(itemID int64, price float64, status string) (*Item, error) {
	_, err := d.Exec("UPDATE items SET price = ?, status = ? WHERE id = ?", price, status, itemID)
	if err != nil {
		return nil, err
	}
	
	// Return the updated item
	var i Item
	err = d.QueryRow("SELECT id, family_id, name, quantity, price, status FROM items WHERE id = ?", itemID).Scan(&i.ID, &i.FamilyID, &i.Name, &i.Quantity, &i.Price, &i.Status)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// DeleteItem removes an item from the database
func (d *DB) DeleteItem(itemID int64) (*Item, error) {
	// We need the item details to broadcast the deletion to the right room
	var i Item
	err := d.QueryRow("SELECT id, family_id, name, quantity, price, status FROM items WHERE id = ?", itemID).Scan(&i.ID, &i.FamilyID, &i.Name, &i.Quantity, &i.Price, &i.Status)
	if err != nil {
		return nil, err
	}

	_, err = d.Exec("DELETE FROM items WHERE id = ?", itemID)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// SetCurrency updates the family's preferred currency symbol
func (d *DB) SetCurrency(familyID int64, currency string) error {
	_, err := d.Exec("UPDATE families SET currency = ? WHERE id = ?", currency, familyID)
	return err
}

// MarkFamilyDone sets the family list status to 'done'
func (d *DB) MarkFamilyDone(familyID int64) error {
	_, err := d.Exec("UPDATE families SET status = 'done' WHERE id = ?", familyID)
	return err
}

func (d *DB) CreateMessage(familyID int64, sender, content string) (Message, error) {
	res, err := d.Exec("INSERT INTO messages (family_id, sender, content) VALUES (?, ?, ?)", familyID, sender, content)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()
	return Message{
		ID:       id,
		FamilyID: familyID,
		Sender:   sender,
		Content:  content,
		IsRead:   false,
	}, nil
}

func (d *DB) GetMessages(familyID int64) ([]Message, error) {
	rows, err := d.Query("SELECT id, family_id, sender, content, is_read, created_at FROM messages WHERE family_id = ? ORDER BY created_at ASC", familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.FamilyID, &m.Sender, &m.Content, &m.IsRead, &m.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, nil
}

func (d *DB) MarkMessagesAsRead(familyID int64, viewer string) error {
	// If viewer is 'shop', mark messages from 'family' as read
	// If viewer is 'family', mark messages from 'shop' as read
	targetSender := "shop"
	if viewer == "shop" {
		targetSender = "family"
	}
	_, err := d.Exec("UPDATE messages SET is_read = 1 WHERE family_id = ? AND sender = ? AND is_read = 0", familyID, targetSender)
	return err
}

func (d *DB) GetUnreadCount(familyID int64, viewer string) (int, error) {
	targetSender := "shop"
	if viewer == "shop" {
		targetSender = "family"
	}
	var count int
	err := d.QueryRow("SELECT COUNT(*) FROM messages WHERE family_id = ? AND sender = ? AND is_read = 0", familyID, targetSender).Scan(&count)
	return count, err
}

func (d *DB) UpdateFamilySettings(familyID int64, expiryHours int, language string) error {
	_, err := d.Exec("UPDATE families SET expiry_hours = ?, language = ? WHERE id = ?", expiryHours, language, familyID)
	return err
}

func (d *DB) CleanupExpiredFamilies() (int64, error) {
	// Delete expired families (-1 means never)
	query := `
		DELETE FROM families 
		WHERE expiry_hours != -1 
		AND datetime(created_at, '+' || expiry_hours || ' hours') < datetime('now')
	`
	res, err := d.Exec(query)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteAll clears all data from the database
func (d *DB) DeleteAll() error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tables := []string{"items", "messages", "shops", "families"}
	for _, table := range tables {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
		// Reset autoincrement
		if _, err := tx.Exec("DELETE FROM sqlite_sequence WHERE name = ?", table); err != nil {
			// Ignore if sqlite_sequence doesn't have the entry
		}
	}

	return tx.Commit()
}

// GetStats returns counts for key database entities
func (d *DB) GetStats() (families, items, messages int, err error) {
	err = d.QueryRow("SELECT COUNT(*) FROM families").Scan(&families)
	if err != nil {
		return
	}
	err = d.QueryRow("SELECT COUNT(*) FROM items").Scan(&items)
	if err != nil {
		return
	}
	err = d.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages)
	return
}

// ListFamilies returns all families with their tokens and statuses
func (d *DB) ListFamilies() ([]Family, error) {
	rows, err := d.Query("SELECT id, token, name, status FROM families")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var families []Family
	for rows.Next() {
		var f Family
		if err := rows.Scan(&f.ID, &f.Token, &f.Name, &f.Status); err != nil {
			return nil, err
		}
		families = append(families, f)
	}
	return families, nil
}
