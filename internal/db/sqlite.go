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
	ID        int64
	Token     string
	Name      string
	ShopToken string
}

type Item struct {
	ID       int64
	FamilyID int64
	Name     string
	Quantity string
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
		price REAL DEFAULT 0,
		status TEXT DEFAULT 'pending',
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (family_id) REFERENCES families(id)
	);
	`
	_, err := db.Exec(schema)
	return err
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
		query = `SELECT f.id, f.token, f.name, s.token 
		         FROM families f 
		         JOIN shops s ON f.id = s.family_id 
		         WHERE s.token = ?`
	} else {
		// If using family token, we need to fetch the shop token as well
		query = `SELECT f.id, f.token, f.name, s.token 
		         FROM families f 
		         JOIN shops s ON f.id = s.family_id 
		         WHERE f.token = ?`
	}

	err := d.QueryRow(query, token).Scan(&f.ID, &f.Token, &f.Name, &f.ShopToken)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (d *DB) GetFamilyByTokenFromID(familyID int64) (*Family, error) {
	var f Family
	query := `SELECT f.id, f.token, f.name, s.token 
	          FROM families f 
	          JOIN shops s ON f.id = s.family_id 
	          WHERE f.id = ?`
	err := d.QueryRow(query, familyID).Scan(&f.ID, &f.Token, &f.Name, &f.ShopToken)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// AddItem adds a new item to the list
func (d *DB) AddItem(familyID int64, name, quantity string) (*Item, error) {
	res, err := d.Exec("INSERT INTO items (family_id, name, quantity, status) VALUES (?, ?, ?, 'pending')", familyID, name, quantity)
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
		Status:   "pending",
	}, nil
}

// GetItems retrieves all items for a family
func (d *DB) GetItems(familyID int64) ([]Item, error) {
	rows, err := d.Query("SELECT id, family_id, name, quantity, price, status FROM items WHERE family_id = ? ORDER BY id DESC", familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var i Item
		if err := rows.Scan(&i.ID, &i.FamilyID, &i.Name, &i.Quantity, &i.Price, &i.Status); err != nil {
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
