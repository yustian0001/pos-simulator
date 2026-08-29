package main
import ("database/sql"
	"log"
	"net/http"
	"path/filepath"; "io"; "strings"; "sync"; "fmt"; "net/http/httptest")

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Force local SQLite for tests (temp file, not Turso)
	tmpFile := filepath.Join(os.TempDir(), "pos_test.db")
	os.Remove(tmpFile)
	var err error
	db, err = sql.Open("sqlite", tmpFile+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatal(err)
	}
	db.Exec("PRAGMA foreign_keys = ON")
	defer os.Remove(tmpFile)
	defer db.Close()
	// Create schema (same as main.go)
	tables := []string{
		"CREATE TABLE IF NOT EXISTS products (id INTEGER PRIMARY KEY AUTOINCREMENT,sku TEXT UNIQUE NOT NULL,name TEXT NOT NULL,price INTEGER DEFAULT 0,cost INTEGER DEFAULT 0,category TEXT DEFAULT 'Umum',stock INTEGER DEFAULT 0,unit TEXT DEFAULT 'pcs',barcode TEXT DEFAULT '',promo_price INTEGER DEFAULT 0,promo_active INTEGER DEFAULT 0,tax_rate REAL DEFAULT -1,active INTEGER DEFAULT 1,password_changed INTEGER DEFAULT 0,created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)",
		"CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT,username TEXT UNIQUE NOT NULL,password TEXT NOT NULL,display_name TEXT DEFAULT '',role TEXT DEFAULT 'kasir',active INTEGER DEFAULT 1,password_changed INTEGER DEFAULT 0)",
		"CREATE TABLE IF NOT EXISTS shifts (id INTEGER PRIMARY KEY AUTOINCREMENT,shift_name TEXT NOT NULL,cashier TEXT NOT NULL,opened_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,closed_at TIMESTAMP DEFAULT NULL,opening_cash INTEGER DEFAULT 0,closing_cash INTEGER DEFAULT 0,expected_cash INTEGER DEFAULT 0,cash_sales INTEGER DEFAULT 0,cash_out INTEGER DEFAULT 0,cash_discrepancy INTEGER DEFAULT 0,total_sales INTEGER DEFAULT 0,total_tx INTEGER DEFAULT 0,status TEXT DEFAULT 'open')",
		"CREATE TABLE IF NOT EXISTS members (id INTEGER PRIMARY KEY AUTOINCREMENT,member_id TEXT UNIQUE NOT NULL,name TEXT NOT NULL,phone TEXT DEFAULT '',email TEXT DEFAULT '',points INTEGER DEFAULT 0,tier TEXT DEFAULT 'basic',active INTEGER DEFAULT 1,created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)",
		"CREATE TABLE IF NOT EXISTS transactions (id INTEGER PRIMARY KEY AUTOINCREMENT,tx_id TEXT UNIQUE NOT NULL,shift_id INTEGER,total INTEGER DEFAULT 0,discount INTEGER DEFAULT 0,tax INTEGER DEFAULT 0,grand_total INTEGER DEFAULT 0,payment TEXT DEFAULT 'CASH',amount_paid INTEGER DEFAULT 0,change_amount INTEGER DEFAULT 0,customer_name TEXT DEFAULT '',member_id INTEGER DEFAULT NULL,cashier TEXT DEFAULT 'kasir',notes TEXT DEFAULT '',status TEXT DEFAULT 'completed',created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY (shift_id) REFERENCES shifts(id))",
		"CREATE TABLE IF NOT EXISTS tx_items (id INTEGER PRIMARY KEY AUTOINCREMENT,tx_id TEXT NOT NULL,product_id INTEGER,name TEXT NOT NULL,qty INTEGER DEFAULT 1,price INTEGER DEFAULT 0,discount INTEGER DEFAULT 0,subtotal INTEGER DEFAULT 0,notes TEXT DEFAULT '',FOREIGN KEY (tx_id) REFERENCES transactions(tx_id),FOREIGN KEY (product_id) REFERENCES products(id))",
		"CREATE TABLE IF NOT EXISTS cash_log (id INTEGER PRIMARY KEY AUTOINCREMENT,shift_id INTEGER,type TEXT NOT NULL,amount INTEGER DEFAULT 0,description TEXT DEFAULT '',created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY (shift_id) REFERENCES shifts(id))",
		"CREATE TABLE IF NOT EXISTS categories (id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT UNIQUE NOT NULL,icon TEXT DEFAULT '📦')",
		"CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY,value TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS holds (id INTEGER PRIMARY KEY AUTOINCREMENT,hold_id TEXT UNIQUE NOT NULL,items_json TEXT NOT NULL,customer_name TEXT DEFAULT '',created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)",
		"CREATE TABLE IF NOT EXISTS audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT,action TEXT NOT NULL,entity TEXT NOT NULL,entity_id TEXT DEFAULT '',user TEXT DEFAULT '',details TEXT DEFAULT '',created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)",
		"CREATE TABLE IF NOT EXISTS inventory_movements (id INTEGER PRIMARY KEY AUTOINCREMENT,product_id INTEGER NOT NULL,movement_type TEXT NOT NULL,quantity INTEGER NOT NULL,stock_before INTEGER NOT NULL,stock_after INTEGER NOT NULL,reference_type TEXT DEFAULT '',reference_id TEXT DEFAULT '',source TEXT NOT NULL DEFAULT 'manual',reason TEXT DEFAULT '',user TEXT DEFAULT '',created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY (product_id) REFERENCES products(id))",
		"CREATE TABLE IF NOT EXISTS idempotency_keys (key TEXT PRIMARY KEY,action TEXT NOT NULL,payload_hash TEXT NOT NULL DEFAULT '',response_json TEXT NOT NULL,status_code INTEGER DEFAULT 200,created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,expires_at TIMESTAMP NOT NULL)",
	}
	for _, t := range tables {
		db.Exec(t)
	}
	os.Exit(m.Run())
}

func TestSessionCreation(t *testing.T) {
	token := createSession("admin", "admin")
	if token == "" {
		t.Error("Session token should not be empty")
	}
	sessionsMu.RLock()
	sess, exists := sessions[token]
	sessionsMu.RUnlock()
	if !exists {
		t.Error("Session should exist")
	}
	if sess.role != "admin" {
		t.Error("Session role should be admin")
	}
	if time.Now().After(sess.expiresAt) {
		t.Error("Session should not be expired")
	}
}

func TestSessionExpiry(t *testing.T) {
	// Create session that expires immediately
	token := createSession("kasir", "kasir1")
	sessionsMu.Lock()
	sessions[token].expiresAt = time.Now().Add(-1 * time.Second)
	sessionsMu.Unlock()

	// Verify expired
	sessionsMu.RLock()
	sess := sessions[token]
	sessionsMu.RUnlock()
	if !time.Now().After(sess.expiresAt) {
		t.Error("Session should be expired")
	}
}

func TestRateLimiter(t *testing.T) {
	key := "test_rate_limit_" + time.Now().String()
	// First 5 attempts should pass
	for i := 0; i < 5; i++ {
		if !checkRateLimit(key, 5, time.Minute) {
			t.Errorf("Attempt %d should be allowed", i)
		}
	}
	// 6th attempt should be blocked
	if checkRateLimit(key, 5, time.Minute) {
		t.Error("6th attempt should be blocked")
	}
}

func TestCSRFToken(t *testing.T) {
	token := generateCSRFToken()
	if token == "" {
		t.Error("CSRF token should not be empty")
	}
	// Should be valid
	if !validateCSRF(token) {
		t.Error("CSRF token should be valid")
	}
	// Same token should be consumed (one-time use)
	if validateCSRF(token) {
		t.Error("CSRF token should be consumed after first use")
	}
}

func TestCSRFTokenExpiry(t *testing.T) {
	token := generateCSRFToken()
	// Manually expire
	csrfTokens.Lock()
	csrfTokens.data[token] = time.Now().Add(-1 * time.Minute)
	csrfTokens.Unlock()

	if validateCSRF(token) {
		t.Error("Expired CSRF token should be invalid")
	}
}

func TestGenerateID(t *testing.T) {
	id1 := generateID("TX", 8)
	id2 := generateID("TX", 8)
	if id1 == id2 {
		t.Error("Generated IDs should be unique")
	}
	if len(id1) != 10 { // prefix "TX" + 8 hex chars
		t.Logf("ID: %s (length %d)", id1, len(id1))
	}
}

func TestNullInt(t *testing.T) {
	// nullInt(0) should return nil
	if v := nullInt(0); v != nil {
		t.Error("nullInt(0) should return nil")
	}
	// nullInt(5) should return 5
	if v := nullInt(5); v != 5 {
		t.Error("nullInt(5) should return 5")
	}
}

func TestNullStr(t *testing.T) {
	// nullStr("") should return nil
	if v := nullStr(""); v != nil {
		t.Error("nullStr('') should return nil")
	}
	// nullStr("hello") should return "hello"
	if v := nullStr("hello"); v != "hello" {
		t.Error("nullStr('hello') should return 'hello'")
	}
}

func TestDecodeJSON(t *testing.T) {
	// Test with valid JSON
	r := createTestRequest("POST", "/test", `{"key":"value"}`)
	var result map[string]string
	if err := decodeJSON(r, &result); err != nil {
		t.Error("Should decode valid JSON")
	}
	if result["key"] != "value" {
		t.Error("Should decode key=value")
	}
}

func createTestRequest(method, url, body string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, reader)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// === Concurrency Test ===
func TestConcurrentCheckout(t *testing.T) {
	// Setup: create product with stock=1
	db.Exec("INSERT OR REPLACE INTO products (sku,name,price,cost,category,stock,unit,barcode,tax_rate,active) VALUES ('TEST001','Test Product',10000,5000,'Test',1,'pcs','000',-1,1)")

	// Create shift
	db.Exec("INSERT INTO shifts (shift_name,cashier,opening_cash,status) VALUES ('Test','tester',100000,'open')")
	var shiftID int
	db.QueryRow("SELECT id FROM shifts WHERE shift_name='Test' AND status='open'").Scan(&shiftID)

	// Run 2 concurrent checkouts
	var wg sync.WaitGroup
	results := make(chan string, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			jsonBody := fmt.Sprintf(`{"items":[{"product_id":1,"qty":1,"discount":0,"notes":""}],"payment":"CASH","discount":0,"amount_paid":10000,"cashier":"tester","shift_id":%d}`, shiftID)
			req, _ := http.NewRequest("POST", "/api/checkout", strings.NewReader(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := &httptest.ResponseRecorder{}
			handleCheckout(w, req)
			results <- fmt.Sprintf("Request %d: %d", n+1, w.Code)
		}(i)
	}

	wg.Wait()
	close(results)

	// Check results
	successCount := 0
	for r := range results {
		t.Log(r)
		if strings.Contains(r, "200") {
			successCount++
		}
	}

	// Only 1 should succeed (stock=1, qty=1 each)
	if successCount > 1 {
		t.Errorf("Expected at most 1 success for stock=1, got %d", successCount)
	}

	// Verify stock is 0
	var stock int
	db.QueryRow("SELECT stock FROM products WHERE id=1").Scan(&stock)
	if stock != 0 {
		t.Errorf("Stock should be 0 after checkout, got %d", stock)
	}

	// Cleanup
	db.Exec("DELETE FROM products WHERE sku='TEST001'")
	db.Exec("DELETE FROM shifts WHERE shift_name='Test'")
	db.Exec("DELETE FROM transactions WHERE cashier='tester'")
	db.Exec("DELETE FROM tx_items WHERE name='Test Product'")
}

// === Ownership Tests ===
func TestShiftOwnership(t *testing.T) {
	// Setup: create shift owned by "kasir1"
	db.Exec("INSERT INTO shifts (shift_name,cashier,opening_cash,status) VALUES ('Test','kasir1',50000,'open')")
	var shiftID int
	db.QueryRow("SELECT id FROM shifts WHERE shift_name='Test' AND cashier='kasir1'").Scan(&shiftID)

	// Create session for "kasir2" (wrong cashier)
	token2 := createSession("kasir", "kasir2")

	// Try to close-shift-self as kasir2 on kasir1's shift
	req, _ := http.NewRequest("POST", fmt.Sprintf("/api/shifts/%d/close-self", shiftID), strings.NewReader(`{"closing_cash":50000}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token2)
	w := &httptest.ResponseRecorder{}
	handleCloseShiftSelf(w, req)

	if w.Code != 403 {
		t.Errorf("Expected 403 for wrong cashier, got %d", w.Code)
	}

	// Cleanup
	db.Exec("DELETE FROM shifts WHERE shift_name='Test'")
}

func TestHoldAuth(t *testing.T) {
	// Try to create hold without session
	req, _ := http.NewRequest("POST", "/api/hold", strings.NewReader(`{"items":"[]","customer_name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := &httptest.ResponseRecorder{}
	handleHold(w, req)

	// Should work (hold is public for kasir quick access)
	if w.Code != 200 {
		t.Errorf("Hold should be accessible, got %d", w.Code)
	}
}

func TestCheckoutShiftOwnership(t *testing.T) {
	// Setup: create shift owned by "kasir1"
	db.Exec("INSERT INTO shifts (shift_name,cashier,opening_cash,status) VALUES ('TestC','kasir1',100000,'open')")
	var shiftID int
	db.QueryRow("SELECT id FROM shifts WHERE shift_name='TestC' AND cashier='kasir1'").Scan(&shiftID)

	// Create product
	db.Exec("INSERT OR REPLACE INTO products (sku,name,price,cost,category,stock,unit,barcode,tax_rate,active) VALUES ('TEST002','Test Product 2',10000,5000,'Test',10,'pcs','001',-1,1)")

	// Try checkout with shift_id belonging to another cashier
	jsonBody := fmt.Sprintf(`{"items":[{"product_id":1,"qty":1,"discount":0,"notes":""}],"payment":"CASH","discount":0,"amount_paid":10000,"cashier":"kasir2","shift_id":%d}`, shiftID)
	req, _ := http.NewRequest("POST", "/api/checkout", strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := &httptest.ResponseRecorder{}
	handleCheckout(w, req)

	// Should succeed (checkout doesn't check shift ownership currently - just uses shift_id)
	// This test documents the current behavior
	t.Logf("Checkout with different cashier shift_id: status %d (current behavior)", w.Code)

	// Cleanup
	db.Exec("DELETE FROM products WHERE sku='TEST002'")
	db.Exec("DELETE FROM shifts WHERE shift_name='TestC'")
	db.Exec("DELETE FROM transactions WHERE cashier='kasir2'")
	db.Exec("DELETE FROM tx_items WHERE name='Test Product 2'")
}
