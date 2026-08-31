package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
	"golang.org/x/crypto/bcrypt"
)

var db *sql.DB

// Session store with expiry
type session struct {
	role      string
	username  string
	expiresAt time.Time
}

var (
	sessions   = make(map[string]*session)
	sessionsMu sync.RWMutex
)

func createSession(role string, username string) string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	sessionsMu.Lock()
	sessions[token] = &session{
		role:      role,
		username:  username,
		expiresAt: time.Now().Add(8 * time.Hour), // 8 jam per shift
	}
	sessionsMu.Unlock()
	return token
}

func validateSession(token string) (string, bool) {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	s, ok := sessions[token]
	if !ok {
		return "", false
	}
	if time.Now().After(s.expiresAt) {
		delete(sessions, token)
		return "", false
	}
	return s.role, true
}

func deleteSession(token string) {
	sessionsMu.Lock()
	delete(sessions, token)
	sessionsMu.Unlock()
}

func cleanupSessions() {
	for {
		time.Sleep(5 * time.Minute)
		sessionsMu.Lock()
		now := time.Now()
		for token, s := range sessions {
			if now.After(s.expiresAt) {
				delete(sessions, token)
			}
		}
		sessionsMu.Unlock()
	}
}

// Generate secure random ID
func generateID(prefix string, length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return fmt.Sprintf("%s%s", prefix, hex.EncodeToString(b)[:length*2])
}

type Product struct {
	ID          int     `json:"id"`
	SKU         string  `json:"sku"`
	Name        string  `json:"name"`
	Price       int     `json:"price"`
	Cost        int     `json:"cost,omitempty"`
	Category    string  `json:"category"`
	Stock       int     `json:"stock"`
	Unit        string  `json:"unit"`
	Barcode     string  `json:"barcode"`
	PromoPrice  int     `json:"promo_price"`
	PromoActive int     `json:"promo_active"`
	TaxRate     float64 `json:"tax_rate"`
	Active      int     `json:"active"`
}

type Transaction struct {
	ID         int     `json:"id"`
	TxID       string  `json:"tx_id"`
	ShiftID    *int    `json:"shift_id"`
	Total      int     `json:"total"`
	Discount   int     `json:"discount"`
	Tax        int     `json:"tax"`
	GrandTotal int     `json:"grand_total"`
	Payment    string  `json:"payment"`
	AmountPaid int     `json:"amount_paid"`
	ChangeAmt  int     `json:"change_amount"`
	Customer   string  `json:"customer_name"`
	MemberID   *string `json:"member_id"`
	Cashier    string  `json:"cashier"`
	Notes      string  `json:"notes"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
}

type TxItem struct {
	ID        int    `json:"id"`
	TxID      string `json:"tx_id"`
	ProductID int    `json:"product_id"`
	Name      string `json:"name"`
	Qty       int    `json:"qty"`
	Price     int    `json:"price"`
	Discount  int    `json:"discount"`
	Subtotal  int    `json:"subtotal"`
	Notes     string `json:"notes"`
}

type Shift struct {
	ID           int     `json:"id"`
	ShiftName    string  `json:"shift_name"`
	Cashier      string  `json:"cashier"`
	OpenedAt     string  `json:"opened_at"`
	ClosedAt     *string `json:"closed_at"`
	OpeningCash  int     `json:"opening_cash"`
	ClosingCash  int     `json:"closing_cash"`
	ExpectedCash int     `json:"expected_cash"`
	CashSales    int     `json:"cash_sales"`
	CashOut      int     `json:"cash_out"`
	Discrepancy  int     `json:"cash_discrepancy"`
	TotalSales   int     `json:"total_sales"`
	TotalTx      int     `json:"total_tx"`
	Status       string  `json:"status"`
}

type CashLog struct {
	ID          int    `json:"id"`
	ShiftID     int    `json:"shift_id"`
	Type        string `json:"type"`
	Amount      int    `json:"amount"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

type Member struct {
	ID       int    `json:"id"`
	MemberID string `json:"member_id"`
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Points   int    `json:"points"`
	Tier     string `json:"tier"`
	Active   int    `json:"active"`
}

type User struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	Password    string `json:"-"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Active      int    `json:"active"`
}

type CartItemReq struct {
	ProductID int    `json:"product_id"`
	Qty       int    `json:"qty"`
	Notes     string `json:"notes"`
	Discount  int    `json:"discount"`
}

type CheckoutReq struct {
	Items        []CartItemReq `json:"items"`
	Payment      string        `json:"payment"`
	Discount     int           `json:"discount"`
	AmountPaid   int           `json:"amount_paid"`
	CustomerName string        `json:"customer_name"`
	MemberID     string        `json:"member_id"`
	Cashier      string        `json:"cashier"`
	ShiftID      int           `json:"shift_id"`
	Notes        string        `json:"notes"`
}

type HoldReq struct {
	Items        json.RawMessage `json:"items"`
	CustomerName string          `json:"customer_name"`
}

func getDataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func initDB() {
	// Read config from env vars ONLY (no embedded secrets)
	tursoURL := os.Getenv("TURSO_DATABASE_URL")
	tursoToken := os.Getenv("TURSO_AUTH_TOKEN")
	// Environment variables ONLY - no embedded or file config
	// Set TURSO_DATABASE_URL and TURSO_AUTH_TOKEN as env vars
	if v := os.Getenv("TURSO_DATABASE_URL"); v != "" {
		tursoURL = v
	}
	if v := os.Getenv("TURSO_AUTH_TOKEN"); v != "" {
		tursoToken = v
	}

	if tursoURL != "" && tursoToken != "" {
		dir := getDataDir()
		dbPath := filepath.Join(dir, "pos_replica.db")
		connStr := fmt.Sprintf("file:%s?syncUrl=%s&authToken=%s&syncInterval=60s", dbPath, tursoURL, tursoToken)
		var err error
		db, err = sql.Open("libsql", connStr)
		if err != nil {
			fmt.Printf("[POS] Turso Embedded Replica failed: %v, using local sqlite\n", err)
			db = nil
		} else {
			if pingErr := db.Ping(); pingErr != nil {
				fmt.Printf("[POS] Turso Embedded Replica ping warning: %v (offline mode on %s)\n", pingErr, dbPath)
			} else {
				fmt.Printf("[POS] DB: Turso Embedded Replica active (%s <-> %s)\n", dbPath, tursoURL)
			}
		}
	}

	// Fallback to local SQLite
	if db == nil {
		dir := getDataDir()
		dbPath := filepath.Join(dir, "pos.db")
		var err error
		db, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			log.Fatal(err)
		}
		db.Exec("PRAGMA foreign_keys = ON")
		fmt.Printf("[POS] DB: %s\n", dbPath)
	}

	tables := `
	CREATE TABLE IF NOT EXISTS products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sku TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
		price INTEGER DEFAULT 0, cost INTEGER DEFAULT 0,
		category TEXT DEFAULT 'Umum', stock INTEGER DEFAULT 0,
		unit TEXT DEFAULT 'pcs', barcode TEXT DEFAULT '',
		promo_price INTEGER DEFAULT 0, promo_active INTEGER DEFAULT 0,
		tax_rate REAL DEFAULT -1,
		active INTEGER DEFAULT 1, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL, icon TEXT DEFAULT '📦'
	);
	CREATE TABLE IF NOT EXISTS transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tx_id TEXT UNIQUE NOT NULL, shift_id INTEGER,
		total INTEGER DEFAULT 0, discount INTEGER DEFAULT 0,
		tax INTEGER DEFAULT 0, grand_total INTEGER DEFAULT 0,
		payment TEXT DEFAULT 'CASH', amount_paid INTEGER DEFAULT 0,
		change_amount INTEGER DEFAULT 0, customer_name TEXT DEFAULT '',
		member_id INTEGER DEFAULT NULL, cashier TEXT DEFAULT 'kasir',
		notes TEXT DEFAULT '', status TEXT DEFAULT 'completed',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS tx_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tx_id TEXT NOT NULL, product_id INTEGER,
		name TEXT NOT NULL, qty INTEGER DEFAULT 1,
		price INTEGER DEFAULT 0, discount INTEGER DEFAULT 0,
		subtotal INTEGER DEFAULT 0, notes TEXT DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL, password TEXT NOT NULL,
		display_name TEXT DEFAULT '', role TEXT DEFAULT 'kasir',
		active INTEGER DEFAULT 1
	);
	CREATE TABLE IF NOT EXISTS shifts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		shift_name TEXT NOT NULL, cashier TEXT NOT NULL,
		opened_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		closed_at TIMESTAMP DEFAULT NULL,
		opening_cash INTEGER DEFAULT 0, closing_cash INTEGER DEFAULT 0,
		expected_cash INTEGER DEFAULT 0, cash_sales INTEGER DEFAULT 0,
		cash_out INTEGER DEFAULT 0, cash_discrepancy INTEGER DEFAULT 0,
		total_sales INTEGER DEFAULT 0, total_tx INTEGER DEFAULT 0,
		status TEXT DEFAULT 'open'
	);
	CREATE TABLE IF NOT EXISTS cash_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		shift_id INTEGER, type TEXT NOT NULL,
		amount INTEGER DEFAULT 0, description TEXT DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS members (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		member_id TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
		phone TEXT DEFAULT '', email TEXT DEFAULT '',
		points INTEGER DEFAULT 0, tier TEXT DEFAULT 'basic',
		active INTEGER DEFAULT 1, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS holds (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hold_id TEXT UNIQUE NOT NULL, items_json TEXT NOT NULL,
		customer_name TEXT DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY, value TEXT NOT NULL
	);`
	db.Exec(tables)

	// Create inventory_movements table
	db.Exec(`CREATE TABLE IF NOT EXISTS inventory_movements (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		product_id INTEGER NOT NULL,
		movement_type TEXT NOT NULL,
		quantity INTEGER NOT NULL,
		stock_before INTEGER NOT NULL,
		stock_after INTEGER NOT NULL,
		reference_type TEXT DEFAULT '',
		reference_id TEXT DEFAULT '',
		source TEXT NOT NULL DEFAULT 'manual',
		reason TEXT DEFAULT '',
		user TEXT DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (product_id) REFERENCES products(id)
	)`)

	// Create idempotency_keys table
	db.Exec(`CREATE TABLE IF NOT EXISTS idempotency_keys (
		key TEXT PRIMARY KEY,
		action TEXT NOT NULL,
		response_json TEXT NOT NULL,
		payload_hash TEXT NOT NULL DEFAULT '',
		status_code INTEGER DEFAULT 200,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP NOT NULL
	)`)

	// Schema migrations
	db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		checksum TEXT DEFAULT ''
	)`)

	// Migration: add columns that may not exist in older DBs
	db.Exec("ALTER TABLE users ADD COLUMN password_changed INTEGER DEFAULT 0")
	db.Exec("ALTER TABLE products ADD COLUMN tax_rate REAL DEFAULT -1")

	db.Exec(`CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		action TEXT NOT NULL, entity TEXT DEFAULT '',
		entity_id TEXT DEFAULT '', user TEXT DEFAULT '',
		details TEXT DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	db.Exec("INSERT OR IGNORE INTO schema_migrations (version,name,checksum) VALUES (1,'initial','v2.2')")

	// Seed data
	cats := [][2]string{{"Makanan", "🍜"}, {"Minuman", "🥤"}, {"Snack", "🍿"}, {"Lainnya", "📦"}}
	for _, c := range cats {
		db.Exec("INSERT OR IGNORE INTO categories (name, icon) VALUES (?, ?)", c[0], c[1])
	}

	products := []struct {
		sku, name, cat, unit, barcode string
		price, cost, stock, promoPrice, promoActive int
		taxRate float64
	}{
		{"PRD001", "Nasi Goreng", "Makanan", "pcs", "899001", 25000, 15000, 50, 22000, 1, -1},
		{"PRD002", "Ayam Geprek", "Makanan", "pcs", "899002", 35000, 20000, 30, 0, 0, -1},
		{"PRD003", "Es Teh", "Minuman", "pcs", "899003", 5000, 2000, 100, 0, 0, 0},
		{"PRD004", "Nasi Uduk", "Makanan", "pcs", "899004", 25000, 15000, 40, 0, 0, -1},
		{"PRD005", "Jus Alpukat", "Minuman", "pcs", "899005", 15000, 8000, 25, 0, 0, -1},
		{"PRD006", "Indomie Goreng", "Makanan", "pcs", "899006", 8000, 5000, 80, 0, 0, -1},
		{"PRD007", "Kopi Susu", "Minuman", "pcs", "899007", 18000, 10000, 60, 0, 0, -1},
		{"PRD008", "Keripik Singkong", "Snack", "pcs", "899008", 10000, 5000, 45, 0, 0, -1},
	}
	for _, p := range products {
		db.Exec("INSERT OR IGNORE INTO products (sku,name,price,cost,category,stock,unit,barcode,promo_price,promo_active,tax_rate) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
			p.sku, p.name, p.price, p.cost, p.cat, p.stock, p.unit, p.barcode, p.promoPrice, p.promoActive, p.taxRate)
	}

	// Seed users with bcrypt-hashed passwords
	users := []struct{ u, p, d, r string }{
		{"admin", "admin123", "Admin Utama", "admin"},
		{"kasir1", "kasir123", "Andi", "kasir"},
		{"kasir2", "kasir123", "Budi", "kasir"},
	}
	for _, u := range users {
		hash, _ := bcrypt.GenerateFromPassword([]byte(u.p), 10)
		db.Exec("INSERT OR IGNORE INTO users (username,password,display_name,role) VALUES (?,?,?,?)", u.u, string(hash), u.d, u.r)
	}

	settings := map[string]string{
		"store_name":   "Masjid Jami' Baiturrahman",
		"opening_cash": "500000",
		"store_address": "Jl. Tole Iskandar No.KM. 3, Mekar Jaya, Kec. Sukmajaya, Kota Depok, Jawa Barat 16411",
		"store_phone":  "081234567890",
		"ad_title":     "Promo Spesial Hari Ini!",
		"ad_desc":      "Dapatkan diskon menarik untuk semua produk pilihan",
		"ad_marquee":   "🎉 Promo 17 Agustus! Diskon 17% semua makanan! 🎉 Gratis Es Teh untuk pembelian di atas Rp 50.000! 🎉",
		"ad_cards":     `[{"emoji":"🍜","name":"Nasi Goreng","price":22000,"old_price":25000},{"emoji":"🥤","name":"Es Teh","price":5000,"old_price":null}]`,
		"qris_merchant": "POS Simulator",
		"qris_amount":   "0",
		"ppn_rate":      "11",
	}
	for k, v := range settings {
		db.Exec("INSERT OR IGNORE INTO settings (key,value) VALUES (?,?)", k, v)
	}

	// Seed 5 test members
	members := []struct{ mid, name, phone, email, tier string; points int }{
		{"MEM000001", "Budi Santoso", "081234567890", "budi@email.com", "gold", 5000},
		{"MEM000002", "Siti Rahayu", "082345678901", "siti@email.com", "silver", 3000},
		{"MEM000003", "Andi Pratama", "083456789012", "andi@email.com", "basic", 1500},
		{"MEM000004", "Dewi Lestari", "084567890123", "dewi@email.com", "gold", 7500},
		{"MEM000005", "Rizky Firmansyah", "085678901234", "rizky@email.com", "silver", 2200},
	}
	for _, m := range members {
		db.Exec("INSERT OR IGNORE INTO members (member_id,name,phone,email,points,tier) VALUES (?,?,?,?,?,?)",
			m.mid, m.name, m.phone, m.email, m.points, m.tier)
	}

}

func now() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
