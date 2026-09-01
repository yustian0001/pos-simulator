package main

import (
	"database/sql"
	"encoding/json"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
)

var (
	wsClients = make(map[*websocket.Conn]bool)
	wsMu      sync.Mutex
	checkoutMu sync.Mutex
)

type WSMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

func wsBroadcast(msg WSMessage) {
	wsMu.Lock()
	defer wsMu.Unlock()
	data, _ := json.Marshal(msg)
	for client := range wsClients {
		if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
			client.Close()
			delete(wsClients, client)
		}
	}
}

func jsonResponse(w http.ResponseWriter, data interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// CSRF middleware for state-changing endpoints
func requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Skip CSRF for GET/HEAD/OPTIONS
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next(w, r)
			return
		}
		// Verify session is valid first
		token := r.Header.Get("Authorization")
		if token == "" {
			jsonResponse(w, map[string]string{"error": "Login required"}, 401)
			return
		}
		sessionsMu.RLock()
		sess, exists := sessions[token]
		sessionsMu.RUnlock()
		if !exists || time.Now().After(sess.expiresAt) {
			jsonResponse(w, map[string]string{"error": "Session expired"}, 401)
			return
		}
		// Validate CSRF token
		csrf := r.Header.Get("X-CSRF-Token")
		if csrf == "" {
			csrf = r.FormValue("csrf_token")
		}
		if !validateCSRF(csrf, token) {
			jsonResponse(w, map[string]string{"error": "CSRF token invalid or missing"}, 403)
			return
		}
		next(w, r)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	return json.NewDecoder(r.Body).Decode(v)
}

func parseID(path string) int {
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if id, err := strconv.Atoi(p); err == nil {
			return id
		}
	}
	return 0
}

func logError(context string, err error) {
	if err != nil {
		fmt.Printf("[ERROR] %s: %v\n", context, err)
	}
}

// === Auth ===

func auditLog(action, entity, entityID, user, details string) {
	db.Exec("INSERT INTO audit_log (action,entity,entity_id,user,details) VALUES (?,?,?,?,?)",
		action, entity, entityID, user, details)
}


// === RATE LIMITER ===
var loginAttempts = struct {
	sync.RWMutex
	data map[string][]time.Time
}{data: make(map[string][]time.Time)}

func checkRateLimit(key string, maxAttempts int, window time.Duration) bool {
	loginAttempts.Lock()
	defer loginAttempts.Unlock()
	now := time.Now()
	attempts := loginAttempts.data[key]
	// Remove old attempts
	var valid []time.Time
	for _, t := range attempts {
		if now.Sub(t) < window {
			valid = append(valid, t)
		}
	}
	if len(valid) >= maxAttempts {
		loginAttempts.data[key] = valid
		return false // blocked
	}
	loginAttempts.data[key] = append(valid, now)
	return true // allowed
}

// === CSRF TOKEN ===
var csrfTokens = struct {
	sync.RWMutex
	data map[string]csrfTokenEntry
}{data: make(map[string]csrfTokenEntry)}

type csrfTokenEntry struct {
	sessionToken string
	expiresAt    time.Time
}

func generateCSRFToken(sessionToken string) string {
	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)
	csrfTokens.Lock()
	csrfTokens.data[token] = csrfTokenEntry{sessionToken: sessionToken, expiresAt: time.Now().Add(30 * time.Minute)}
	csrfTokens.Unlock()
	return token
}

func validateCSRF(token string, sessionToken string) bool {
	csrfTokens.RLock()
	entry, exists := csrfTokens.data[token]
	csrfTokens.RUnlock()
	if !exists || time.Now().After(entry.expiresAt) {
		return false
	}
	// Verify CSRF token is bound to this session
	if entry.sessionToken != sessionToken {
		return false
	}
	return true
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w,r, &req); err != nil {
		jsonResponse(w, map[string]string{"status": "error", "message": "Invalid request"}, 400)
		return
	}
	if req.Username == "" || req.Password == "" {
		jsonResponse(w, map[string]string{"status": "error", "message": "Username dan password wajib diisi"}, 400)
		return
	}

	var user User
	err := db.QueryRow("SELECT id,username,password,display_name,role FROM users WHERE username=? AND active=1",
		req.Username).Scan(&user.ID, &user.Username, &user.Password, &user.DisplayName, &user.Role)
	if err != nil {
		jsonResponse(w, map[string]string{"status": "error", "message": "Username atau password salah"}, 401)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		jsonResponse(w, map[string]string{"status": "error", "message": "Username atau password salah"}, 401)
		return
	}

	token := createSession(user.Role, user.Username)
	jsonResponse(w, map[string]string{"status": "ok", "username": user.Username, "display_name": user.DisplayName, "role": user.Role, "token": token}, 200)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token != "" {
		deleteSession(token)
	}
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

func requireAuth(r *http.Request, requiredRole string) bool {
	token := r.Header.Get("Authorization")
	if token == "" {
		return false
	}
	role, ok := validateSession(token)
	if !ok {
		return false
	}
	if requiredRole == "admin" && role != "admin" {
		return false
	}
	return true
}

func handleGetUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id,username,display_name,role,active FROM users")
	if err != nil {
		logError("handleGetUsers", err)
		jsonResponse(w, map[string]string{"error": "Database error"}, 500)
		return
	}
	defer rows.Close()
	var users []map[string]interface{}
	for rows.Next() {
		var id, active int
		var username, display, role string
		rows.Scan(&id, &username, &display, &role, &active)
		users = append(users, map[string]interface{}{"id": id, "username": username, "display_name": display, "role": role, "active": active})
	}
	if users == nil {
		users = []map[string]interface{}{}
	}
	jsonResponse(w, users, 200)
}

// === Products ===
func handleGetProducts(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.URL.Query().Get("admin") == "1"
	q := "SELECT id,sku,name,description,price,category,stock,min_stock,unit,barcode,promo_price,promo_active,tax_rate,active FROM products WHERE active=1"
	if isAdmin {
		q = "SELECT id,sku,name,description,price,cost,category,stock,min_stock,unit,barcode,promo_price,promo_active,tax_rate,active FROM products WHERE active=1"
	}
	var args []interface{}
	if cat := r.URL.Query().Get("category"); cat != "" && cat != "Semua" {
		q += " AND category=?"
		args = append(args, cat)
	}
	if search := r.URL.Query().Get("search"); search != "" {
		q += " AND (name LIKE ? OR barcode LIKE ? OR sku LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s, s)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		logError("handleGetProducts", err)
		jsonResponse(w, map[string]string{"error": "Database error"}, 500)
		return
	}
	defer rows.Close()
	var products []map[string]interface{}
	for rows.Next() {
		if isAdmin {
			var p Product
			rows.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.Price, &p.Cost, &p.Category, &p.Stock, &p.MinStock, &p.Unit, &p.Barcode, &p.PromoPrice, &p.PromoActive, &p.TaxRate, &p.Active)
			products = append(products, map[string]interface{}{"id": p.ID, "sku": p.SKU, "name": p.Name, "description": p.Description, "price": p.Price, "cost": p.Cost, "category": p.Category, "stock": p.Stock, "min_stock": p.MinStock, "unit": p.Unit, "barcode": p.Barcode, "promo_price": p.PromoPrice, "promo_active": p.PromoActive, "tax_rate": p.TaxRate, "active": p.Active})
		} else {
			var id, price, stock, minStock, promoPrice, promoActive, active int
			var sku, name, description, category, unit, barcode string
			var taxRate float64
			rows.Scan(&id, &sku, &name, &description, &price, &category, &stock, &minStock, &unit, &barcode, &promoPrice, &promoActive, &taxRate, &active)
			products = append(products, map[string]interface{}{"id": id, "sku": sku, "name": name, "description": description, "price": price, "category": category, "stock": stock, "min_stock": minStock, "unit": unit, "barcode": barcode, "promo_price": promoPrice, "promo_active": promoActive, "tax_rate": taxRate, "active": active})
		}
	}
	if products == nil {
		products = []map[string]interface{}{}
	}
	jsonResponse(w, products, 200)
}

func handleGetCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id,name,icon FROM categories ORDER BY name")
	if err != nil {
		logError("handleGetCategories", err)
		jsonResponse(w, map[string]string{"error": "Database error"}, 500)
		return
	}
	defer rows.Close()
	var cats []map[string]interface{}
	for rows.Next() {
		var id int
		var name, icon string
		rows.Scan(&id, &name, &icon)
		cats = append(cats, map[string]interface{}{"id": id, "name": name, "icon": icon})
	}
	if cats == nil {
		cats = []map[string]interface{}{}
	}
	jsonResponse(w, cats, 200)
}

func handleAddProduct(w http.ResponseWriter, r *http.Request) {
	var p Product
	if err := decodeJSON(w,r, &p); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}
	if p.Name == "" || p.SKU == "" {
		jsonResponse(w, map[string]string{"error": "Nama dan SKU wajib diisi"}, 400)
		return
	}
	_, err := db.Exec("INSERT INTO products (sku,name,description,price,cost,category,stock,min_stock,unit,barcode,tax_rate) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		p.SKU, p.Name, p.Description, p.Price, p.Cost, p.Category, p.Stock, p.MinStock, p.Unit, p.Barcode, p.TaxRate)
	if err != nil {
		logError("handleAddProduct", err)
		jsonResponse(w, map[string]string{"error": "Gagal tambah produk"}, 500)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

func handleUpdateProduct(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.URL.Path)
	if id == 0 {
		jsonResponse(w, map[string]string{"error": "Invalid ID"}, 400)
		return
	}
	var p Product
	if err := decodeJSON(w,r, &p); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}
	_, err := db.Exec("UPDATE products SET name=?,description=?,price=?,cost=?,category=?,stock=?,min_stock=?,unit=?,barcode=?,promo_price=?,promo_active=?,tax_rate=? WHERE id=?",
		p.Name, p.Description, p.Price, p.Cost, p.Category, p.Stock, p.MinStock, p.Unit, p.Barcode, p.PromoPrice, p.PromoActive, p.TaxRate, id)
	if err != nil {
		logError("handleUpdateProduct", err)
		jsonResponse(w, map[string]string{"error": "Gagal update produk"}, 500)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

func handleDeleteProduct(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.URL.Path)
	if id == 0 {
		jsonResponse(w, map[string]string{"error": "Invalid ID"}, 400)
		return
	}
	_, err := db.Exec("UPDATE products SET active=0 WHERE id=?", id)
	if err != nil {
		logError("handleDeleteProduct", err)
		jsonResponse(w, map[string]string{"error": "Gagal hapus produk"}, 500)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

// === Shifts ===
func handleOpenShift(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cashier     string `json:"cashier"`
		ShiftName   string `json:"shift_name"`
		OpeningCash int    `json:"opening_cash"`
	}
	if err := decodeJSON(w,r, &req); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}
	if req.OpeningCash == 0 {
		req.OpeningCash = 500000
	}
	res, err := db.Exec("INSERT INTO shifts (shift_name,cashier,opening_cash,status) VALUES (?,?,?,?)",
		req.ShiftName, req.Cashier, req.OpeningCash, "open")
	if err != nil {
		logError("handleOpenShift", err)
		jsonResponse(w, map[string]string{"error": "Gagal buka shift"}, 500)
		return
	}
	sid, _ := res.LastInsertId()
	db.Exec("INSERT INTO cash_log (shift_id,type,amount,description) VALUES (?,?,?,?)",
		sid, "opening", req.OpeningCash, fmt.Sprintf("Opening cash shift %s", req.ShiftName))
	jsonResponse(w, map[string]interface{}{"status": "ok", "shift_id": sid, "opening_cash": req.OpeningCash}, 200)
}

func handleGetShifts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	var rows *sql.Rows
	var err error
	if status != "" {
		rows, err = db.Query("SELECT id,shift_name,cashier,opened_at,closed_at,opening_cash,closing_cash,expected_cash,cash_sales,cash_out,cash_discrepancy,total_sales,total_tx,status FROM shifts WHERE status=? ORDER BY opened_at DESC", status)
	} else {
		rows, err = db.Query("SELECT id,shift_name,cashier,opened_at,closed_at,opening_cash,closing_cash,expected_cash,cash_sales,cash_out,cash_discrepancy,total_sales,total_tx,status FROM shifts ORDER BY opened_at DESC LIMIT 50")
	}
	if err != nil {
		logError("handleGetShifts", err)
		jsonResponse(w, map[string]string{"error": "Database error"}, 500)
		return
	}
	defer rows.Close()
	var shifts []Shift
	for rows.Next() {
		var s Shift
		rows.Scan(&s.ID, &s.ShiftName, &s.Cashier, &s.OpenedAt, &s.ClosedAt, &s.OpeningCash, &s.ClosingCash, &s.ExpectedCash, &s.CashSales, &s.CashOut, &s.Discrepancy, &s.TotalSales, &s.TotalTx, &s.Status)
		shifts = append(shifts, s)
	}
	if shifts == nil {
		shifts = []Shift{}
	}
	for i := range shifts {
		if shifts[i].Status == "open" {
			var cs int
			db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE shift_id=? AND payment='CASH' AND status='completed'", shifts[i].ID).Scan(&cs)
			shifts[i].CashSales = cs
		}
	}
	jsonResponse(w, shifts, 200)
}

func handleGetActiveShifts(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id,shift_name,cashier,opened_at,closed_at,opening_cash,closing_cash,expected_cash,cash_sales,cash_out,cash_discrepancy,total_sales,total_tx,status FROM shifts WHERE status='open'")
	if err != nil {
		logError("handleGetActiveShifts", err)
		jsonResponse(w, map[string]string{"error": "Database error"}, 500)
		return
	}
	defer rows.Close()
	var shifts []Shift
	for rows.Next() {
		var s Shift
		rows.Scan(&s.ID, &s.ShiftName, &s.Cashier, &s.OpenedAt, &s.ClosedAt, &s.OpeningCash, &s.ClosingCash, &s.ExpectedCash, &s.CashSales, &s.CashOut, &s.Discrepancy, &s.TotalSales, &s.TotalTx, &s.Status)
		var cs int
		db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE shift_id=? AND payment='CASH' AND status='completed'", s.ID).Scan(&cs)
		s.CashSales = cs
		shifts = append(shifts, s)
	}
	if shifts == nil {
		shifts = []Shift{}
	}
	jsonResponse(w, shifts, 200)
}

func handleCloseShift(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.URL.Path)
	if id == 0 {
		jsonResponse(w, map[string]string{"error": "Invalid ID"}, 400)
		return
	}
	var req struct {
		ClosingCash int `json:"closing_cash"`
	}
	if err := decodeJSON(w,r, &req); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}

	var shift Shift
	err := db.QueryRow("SELECT id,opening_cash,shift_name FROM shifts WHERE id=? AND status='open'", id).
		Scan(&shift.ID, &shift.OpeningCash, &shift.ShiftName)
	if err != nil {
		jsonResponse(w, map[string]string{"error": "Shift tidak ditemukan atau sudah ditutup"}, 404)
		return
	}

	var cashSales, qrisSales, cashOut, totalSales, totalTx int
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE shift_id=? AND payment='CASH' AND status='completed'", id).Scan(&cashSales)
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE shift_id=? AND payment!='CASH' AND status='completed'", id).Scan(&qrisSales)
	db.QueryRow("SELECT COALESCE(SUM(amount),0) FROM cash_log WHERE shift_id=? AND type='cash_out'", id).Scan(&cashOut)
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0), COUNT(*) FROM transactions WHERE shift_id=? AND status='completed'", id).Scan(&totalSales, &totalTx)

	expected := shift.OpeningCash + cashSales - cashOut
	closingCash := expected
	discrepancy := 0
	if req.ClosingCash > 0 {
		closingCash = req.ClosingCash
		discrepancy = closingCash - expected
	}

	db.Exec("UPDATE shifts SET closed_at=?,closing_cash=?,expected_cash=?,cash_sales=?,cash_out=?,cash_discrepancy=?,total_sales=?,total_tx=?,status='closed' WHERE id=?",
		now(), closingCash, expected, cashSales, cashOut, discrepancy, totalSales, totalTx, id)
	if qrisSales > 0 {
		db.Exec("INSERT INTO cash_log (shift_id,type,amount,description) VALUES (?,?,?,?)",
			id, "qris_sales", qrisSales, fmt.Sprintf("Penjualan QRIS/Non-Tunai shift %s", shift.ShiftName))
	}
	db.Exec("INSERT INTO cash_log (shift_id,type,amount,description) VALUES (?,?,?,?)",
		id, "closing", closingCash, fmt.Sprintf("Closing cash shift %s", shift.ShiftName))

	jsonResponse(w, map[string]interface{}{"status": "ok", "expected": expected, "closing": closingCash, "discrepancy": discrepancy}, 200)
}

func handleCloseShiftSelf(w http.ResponseWriter, r *http.Request) {
	// Session check
	token, _ := getSessionUser(r)
	if token == "" {
		jsonResponse(w, map[string]string{"error": "Login required"}, 401)
		return
	}
	sessionsMu.RLock()
	sess, ok := sessions[token]
	sessionsMu.RUnlock()
	if !ok || sess == nil {
		jsonResponse(w, map[string]string{"error": "Login required"}, 401)
		return
	}

	id := parseID(r.URL.Path)
	if id == 0 {
		jsonResponse(w, map[string]string{"error": "Invalid ID"}, 400)
		return
	}

	var req struct {
		ClosingCash int `json:"closing_cash"`
	}
	decodeJSON(w,r, &req)

	var shift Shift
	err := db.QueryRow("SELECT id,opening_cash,shift_name FROM shifts WHERE id=? AND status='open'", id).
		Scan(&shift.ID, &shift.OpeningCash, &shift.ShiftName)
	if err != nil {
		jsonResponse(w, map[string]string{"error": "Shift tidak ditemukan atau sudah ditutup"}, 404)
		return
	}

	var shiftCashier string
	db.QueryRow("SELECT cashier FROM shifts WHERE id=?", id).Scan(&shiftCashier)
	if sess.role != "admin" && shiftCashier != "" && shiftCashier != sess.username && shiftCashier != sess.role {
		jsonResponse(w, map[string]string{"error": "Shift bukan milik kasir ini"}, 403)
		return
	}

	var cashSales, qrisSales, cashOut, totalSales, totalTx int
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE shift_id=? AND payment='CASH' AND status='completed'", id).Scan(&cashSales)
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE shift_id=? AND payment!='CASH' AND status='completed'", id).Scan(&qrisSales)
	db.QueryRow("SELECT COALESCE(SUM(amount),0) FROM cash_log WHERE shift_id=? AND type='cash_out'", id).Scan(&cashOut)
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0), COUNT(*) FROM transactions WHERE shift_id=? AND status='completed'", id).Scan(&totalSales, &totalTx)

	expected := shift.OpeningCash + cashSales - cashOut
	closingCash := expected
	discrepancy := 0
	if req.ClosingCash > 0 {
		closingCash = req.ClosingCash
		discrepancy = closingCash - expected
	}

	db.Exec("UPDATE shifts SET closed_at=?,closing_cash=?,expected_cash=?,cash_sales=?,cash_out=?,cash_discrepancy=?,total_sales=?,total_tx=?,status='closed' WHERE id=?",
		now(), closingCash, expected, cashSales, cashOut, discrepancy, totalSales, totalTx, id)
	if qrisSales > 0 {
		db.Exec("INSERT INTO cash_log (shift_id,type,amount,description) VALUES (?,?,?,?)",
			id, "qris_sales", qrisSales, fmt.Sprintf("Penjualan QRIS/Non-Tunai shift %s", shift.ShiftName))
	}
	db.Exec("INSERT INTO cash_log (shift_id,type,amount,description) VALUES (?,?,?,?)",
		id, "closing", closingCash, fmt.Sprintf("Closing shift %s (auto)", shift.ShiftName))

	jsonResponse(w, map[string]interface{}{"status": "ok", "expected": expected, "closing": closingCash, "discrepancy": discrepancy}, 200)
}

// === Cash ===
func handleCashDrop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShiftID     int    `json:"shift_id"`
		Amount      int    `json:"amount"`
		Description string `json:"description"`
	}
	decodeJSON(w,r, &req)
	if req.Description == "" {
		req.Description = "Cash drop ke bank"
	}
	db.Exec("INSERT INTO cash_log (shift_id,type,amount,description) VALUES (?,?,?,?)", req.ShiftID, "cash_drop", req.Amount, req.Description)
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

func handleCashIn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShiftID     int    `json:"shift_id"`
		Amount      int    `json:"amount"`
		Description string `json:"description"`
	}
	decodeJSON(w,r, &req)
	db.Exec("INSERT INTO cash_log (shift_id,type,amount,description) VALUES (?,?,?,?)", req.ShiftID, "cash_in", req.Amount, req.Description)
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

func handleGetCashLog(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.URL.Path)
	rows, _ := db.Query("SELECT id,shift_id,type,amount,description,created_at FROM cash_log WHERE shift_id=? ORDER BY created_at", id)
	defer rows.Close()
	var logs []CashLog
	for rows.Next() {
		var l CashLog
		rows.Scan(&l.ID, &l.ShiftID, &l.Type, &l.Amount, &l.Description, &l.CreatedAt)
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []CashLog{}
	}
	jsonResponse(w, logs, 200)
}

// === Members ===
func handleGetMembers(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")
	var rows *sql.Rows
	var err error
	if search != "" {
		s := "%" + search + "%"
		rows, err = db.Query("SELECT id,member_id,name,phone,email,points,tier,active FROM members WHERE active=1 AND (name LIKE ? OR phone LIKE ? OR member_id LIKE ?)", s, s, s)
	} else {
		rows, err = db.Query("SELECT id,member_id,name,phone,email,points,tier,active FROM members WHERE active=1 ORDER BY name")
	}
	if err != nil {
		logError("handleGetMembers", err)
		jsonResponse(w, map[string]string{"error": "Database error"}, 500)
		return
	}
	defer rows.Close()
	var members []Member
	for rows.Next() {
		var m Member
		rows.Scan(&m.ID, &m.MemberID, &m.Name, &m.Phone, &m.Email, &m.Points, &m.Tier, &m.Active)
		members = append(members, m)
	}
	if members == nil {
		members = []Member{}
	}
	jsonResponse(w, members, 200)
}

func handleAddMember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		Email string `json:"email"`
	}
	decodeJSON(w,r, &req)
	mid := generateID("MEM", 6)
	db.Exec("INSERT INTO members (member_id,name,phone,email) VALUES (?,?,?,?)", mid, req.Name, req.Phone, req.Email)
	jsonResponse(w, map[string]string{"status": "ok", "member_id": mid}, 200)
}

func handleGetMember(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	mid := parts[len(parts)-1]
	var m Member
	// Search by member_id OR name (LIKE)
	err := db.QueryRow("SELECT id,member_id,name,phone,email,points,tier FROM members WHERE member_id=? OR name LIKE ?", mid, "%"+mid+"%").
		Scan(&m.ID, &m.MemberID, &m.Name, &m.Phone, &m.Email, &m.Points, &m.Tier)
	if err != nil {
		jsonResponse(w, map[string]string{"error": "Member not found"}, 404)
		return
	}
	jsonResponse(w, m, 200)
}

// === Checkout (with transaction + stock check) ===
func handleCheckout(w http.ResponseWriter, r *http.Request) {
	var req CheckoutReq
	decodeJSON(w,r, &req)

	if len(req.Items) == 0 {
		jsonResponse(w, map[string]string{"error": "Cart kosong"}, 400)
		return
	}

	txID := generateID("TX", 8)
	total := 0
	type checkoutItem struct {
		Name     string  `json:"name"`
		Qty      int     `json:"qty"`
		Price    int     `json:"price"`
		Discount int     `json:"discount"`
		Subtotal int     `json:"subtotal"`
		TaxRate  float64 `json:"tax_rate"`
		Notes    string  `json:"notes"`
	}
	// Prevent concurrent checkout
	checkoutMu.Lock()
	defer checkoutMu.Unlock()

	var items []checkoutItem
	var failedItems []string

	// Use transaction for stock deduction
	sqlTx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelDefault})
	if err != nil {
		logError("checkout begin", err)
		jsonResponse(w, map[string]string{"error": "Database error"}, 500)
		return
	}
	defer sqlTx.Rollback()

	for _, ci := range req.Items {
		if ci.Qty <= 0 {
			continue
		}
		var p Product
		err := sqlTx.QueryRow("SELECT id,name,price,promo_price,promo_active,stock,tax_rate FROM products WHERE id=? AND active=1", ci.ProductID).
			Scan(&p.ID, &p.Name, &p.Price, &p.PromoPrice, &p.PromoActive, &p.Stock, &p.TaxRate)
		if err != nil {
			failedItems = append(failedItems, fmt.Sprintf("Produk ID %d tidak ditemukan", ci.ProductID))
			continue
		}
		if p.Stock < ci.Qty {
			failedItems = append(failedItems, fmt.Sprintf("%s (stok: %d, diminta: %d)", p.Name, p.Stock, ci.Qty))
			continue
		}
		effectivePrice := p.Price
		if p.PromoActive == 1 && p.PromoPrice > 0 {
			effectivePrice = p.PromoPrice
		}
		var itemDiscount int
		if ci.DiscountType == "percent" {
			itemDiscount = int(math.Round(float64(effectivePrice*ci.Qty) * float64(ci.Discount) / 100))
		} else {
			itemDiscount = ci.Discount * ci.Qty
		}
		sub := effectivePrice*ci.Qty - itemDiscount
		total += sub
		items = append(items, checkoutItem{Name: p.Name, Qty: ci.Qty, Price: effectivePrice, Discount: ci.Discount, Subtotal: sub, TaxRate: p.TaxRate, Notes: ci.Notes})
		sqlTx.Exec("INSERT INTO tx_items (tx_id,product_id,name,qty,price,discount,subtotal,notes) VALUES (?,?,?,?,?,?,?,?)",
			txID, ci.ProductID, p.Name, ci.Qty, effectivePrice, ci.Discount, sub, ci.Notes)
		sqlTx.Exec("UPDATE products SET stock=stock-? WHERE id=? AND stock>=?", ci.Qty, ci.ProductID, ci.Qty)
		sqlTx.Exec("INSERT INTO inventory_movements (product_id,movement_type,quantity,stock_before,stock_after,reference_type,reference_id,source,reason,user) VALUES (?,?,?,?,?,?,?,?,?,?)",
			ci.ProductID, "sale", -ci.Qty, p.Stock, p.Stock-ci.Qty, "transaction", txID, "checkout", "Sale via checkout", req.Cashier)
	}

	if len(items) == 0 {
		jsonResponse(w, map[string]string{"error": "Tidak ada produk valid"}, 400)
		return
	}

	discount := req.Discount
	// Validate discount
	if discount < 0 {
		jsonResponse(w, map[string]string{"error": "Diskon tidak boleh negatif"}, 400)
		return
	}
	if req.DiscountType == "percent" {
		if req.Discount > 100 {
			jsonResponse(w, map[string]string{"error": "Diskon persen tidak boleh lebih dari 100%"}, 400)
			return
		}
		discount = int(math.Round(float64(total) * float64(req.Discount) / 100))
	}
	// Per-product tax calculation
	var globalTaxRate float64 = 11
	var ppnStr string
	sqlTx.QueryRow("SELECT value FROM settings WHERE key='ppn_rate'").Scan(&ppnStr)
	if ppnStr != "" {
		if v, err := strconv.ParseFloat(ppnStr, 64); err == nil {
			globalTaxRate = v
		}
	}
	totalTax := 0
	for _, it := range items {
		var taxRate float64
		if it.TaxRate >= 0 {
			taxRate = it.TaxRate
		} else {
			taxRate = globalTaxRate
		}
		totalTax += int(math.Round(float64(it.Subtotal) * taxRate / 100))
	}
	tax := totalTax
	grandTotal := total - discount + tax
	amountPaid := req.AmountPaid
	if amountPaid == 0 {
		amountPaid = grandTotal
	}
	change := amountPaid - grandTotal
	if change < 0 {
		change = 0
	}

	sqlTx.Exec("INSERT INTO transactions (tx_id,shift_id,total,discount,tax,grand_total,payment,amount_paid,change_amount,customer_name,member_id,cashier,notes) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
		txID, nullInt(req.ShiftID), total, discount, tax, grandTotal, req.Payment, amountPaid, change,
		req.CustomerName, nullStr(req.MemberID), req.Cashier, req.Notes)

	if req.ShiftID > 0 {
		if req.Payment == "CASH" {
			sqlTx.Exec("UPDATE shifts SET total_sales=total_sales+?, cash_sales=cash_sales+?, total_tx=total_tx+1 WHERE id=?", grandTotal, grandTotal, req.ShiftID)
		} else {
			sqlTx.Exec("UPDATE shifts SET total_sales=total_sales+?, total_tx=total_tx+1 WHERE id=?", grandTotal, req.ShiftID)
		}
	}
	if req.MemberID != "" {
		sqlTx.Exec("UPDATE members SET points=points+? WHERE member_id=?", grandTotal/1000, req.MemberID)
	}

	if err := sqlTx.Commit(); err != nil {
		logError("checkout commit", err)
		jsonResponse(w, map[string]string{"error": "Gagal simpan transaksi"}, 500)
		return
	}
	auditLog("checkout", "transaction", txID, req.Cashier, fmt.Sprintf("Total: %d, Payment: %s", grandTotal, req.Payment))

	txData := map[string]interface{}{
		"id": txID, "total": total, "discount": discount, "tax": tax,
		"grand_total": grandTotal, "payment": req.Payment,
		"amount_paid": amountPaid, "change": change, "items": items,
		"customer": req.CustomerName, "cashier": req.Cashier, "shift_id": req.ShiftID,
		"time": time.Now().Format("15:04:05"), "date": time.Now().Format("2006-01-02"),
	}

	wsBroadcast(WSMessage{Type: "new_transaction", Data: txData})
	jsonResponse(w, txData, 200)
}

func nullInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func nullStr(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}

// === Hold ===
func handleHold(w http.ResponseWriter, r *http.Request) {
	token, _ := getSessionUser(r)
	if token == "" {
		jsonResponse(w, map[string]string{"error": "Login required"}, 401)
		return
	}
	var req HoldReq
	decodeJSON(w,r, &req)
	holdID := generateID("H", 6)
	db.Exec("INSERT INTO holds (hold_id,items_json,customer_name) VALUES (?,?,?)", holdID, string(req.Items), req.CustomerName)
	jsonResponse(w, map[string]string{"status": "ok", "hold_id": holdID}, 200)
}

func handleGetHolds(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query("SELECT id,hold_id,items_json,customer_name,created_at FROM holds ORDER BY created_at DESC")
	defer rows.Close()
	var holds []map[string]interface{}
	for rows.Next() {
		var id int
		var holdID, itemsJSON, cust, created string
		rows.Scan(&id, &holdID, &itemsJSON, &cust, &created)
		var items interface{}
		json.Unmarshal([]byte(itemsJSON), &items)
		holds = append(holds, map[string]interface{}{"id": id, "hold_id": holdID, "items": items, "customer_name": cust, "created_at": created})
	}
	if holds == nil {
		holds = []map[string]interface{}{}
	}
	jsonResponse(w, holds, 200)
}

func handleDeleteHold(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.URL.Path)
	db.Exec("DELETE FROM holds WHERE id=?", id)
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

// === Transactions ===
func handleGetTransactions(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil {
			limit = v
		}
	}
	q := "SELECT id,tx_id,shift_id,total,discount,tax,grand_total,payment,amount_paid,change_amount,customer_name,cashier,notes,status,created_at FROM transactions WHERE 1=1"
	var args []interface{}
	if date := r.URL.Query().Get("date"); date != "" {
		q += " AND created_at LIKE ?"
		args = append(args, date+"%")
	}
	q += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, _ := db.Query(q, args...)
	defer rows.Close()
	var txs []map[string]interface{}
	for rows.Next() {
		var t Transaction
		rows.Scan(&t.ID, &t.TxID, &t.ShiftID, &t.Total, &t.Discount, &t.Tax, &t.GrandTotal, &t.Payment, &t.AmountPaid, &t.ChangeAmt, &t.Customer, &t.Cashier, &t.Notes, &t.Status, &t.CreatedAt)
		itemRows, _ := db.Query("SELECT id,tx_id,product_id,name,qty,price,discount,subtotal,notes FROM tx_items WHERE tx_id=?", t.TxID)
		var items []TxItem
		for itemRows.Next() {
			var it TxItem
			itemRows.Scan(&it.ID, &it.TxID, &it.ProductID, &it.Name, &it.Qty, &it.Price, &it.Discount, &it.Subtotal, &it.Notes)
			items = append(items, it)
		}
		itemRows.Close()
		txMap := map[string]interface{}{
			"id": t.ID, "tx_id": t.TxID, "shift_id": t.ShiftID, "total": t.Total,
			"discount": t.Discount, "tax": t.Tax, "grand_total": t.GrandTotal,
			"payment": t.Payment, "cashier": t.Cashier, "status": t.Status,
			"created_at": t.CreatedAt, "items": items,
		}
		txs = append(txs, txMap)
	}
	if txs == nil {
		txs = []map[string]interface{}{}
	}
	jsonResponse(w, txs, 200)
}

func handleVoidTransaction(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(r, "") {
		jsonResponse(w, map[string]string{"error": "Login required"}, 401)
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	txID := parts[len(parts)-2]

	// Check if already voided
	var status string
	err := db.QueryRow("SELECT status FROM transactions WHERE tx_id=?", txID).Scan(&status)
	if err != nil {
		jsonResponse(w, map[string]string{"error": "Transaksi tidak ditemukan"}, 404)
		return
	}
	if status == "voided" {
		jsonResponse(w, map[string]interface{}{"status": "ok", "message": "Already voided (idempotent)"}, 200)
		return
	}

	// Get transaction details for reversal
	var txGrandTotal, txMemberID int
	var txPayment string
	db.QueryRow("SELECT grand_total, member_id, payment FROM transactions WHERE tx_id=?", txID).Scan(&txGrandTotal, &txMemberID, &txPayment)

	// Begin transaction for reversal
	voidTx, _ := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelDefault})
	defer voidTx.Rollback()

	// 1. Update status
	voidTx.Exec("UPDATE transactions SET status='voided', notes='voided by admin' WHERE tx_id=? AND status='completed'", txID)

	// 2. Reverse stock via inventory movements
	voidRows, _ := voidTx.Query("SELECT product_id, qty FROM tx_items WHERE tx_id=?", txID)
	var voidedItems []struct{ ProductID, Qty int }
	for voidRows.Next() {
		var item struct{ ProductID, Qty int }
		voidRows.Scan(&item.ProductID, &item.Qty)
		voidedItems = append(voidedItems, item)
		var currentStock int
		voidTx.QueryRow("SELECT stock FROM products WHERE id=?", item.ProductID).Scan(&currentStock)
		voidTx.Exec("UPDATE products SET stock=stock+? WHERE id=?", item.Qty, item.ProductID)
		voidTx.Exec("INSERT INTO inventory_movements (product_id,movement_type,quantity,stock_before,stock_after,reference_type,reference_id,source,reason,user) VALUES (?,?,?,?,?,?,?,?,?,?)",
			item.ProductID, "sale_reversal", item.Qty, currentStock, currentStock+item.Qty, "transaction", txID, "void", "Void reversal", "admin")
	}
	voidRows.Close()

	// 3. Reverse member points
	if txMemberID > 0 {
		voidTx.Exec("UPDATE members SET points=points-? WHERE id=? AND points>=?", txGrandTotal/1000, txMemberID, txGrandTotal/1000)
	}

	// 4. Reverse cash/shift totals
	var txShiftID int
	db.QueryRow("SELECT shift_id FROM transactions WHERE tx_id=?", txID).Scan(&txShiftID)
	if txShiftID > 0 {
		voidTx.Exec("UPDATE shifts SET total_sales=total_sales-?, total_tx=total_tx-1 WHERE id=?", txGrandTotal, txShiftID)
		if txPayment == "CASH" {
			voidTx.Exec("UPDATE shifts SET cash_sales=cash_sales-? WHERE id=?", txGrandTotal, txShiftID)
		}
	}

	// 5. Audit
	voidTx.Exec("INSERT INTO audit_log (action,entity,entity_id,user,details) VALUES (?,?,?,?,?)",
		"void", "transaction", txID, "admin", fmt.Sprintf("Voided. Reversed stock for %d items, points %d, amount %d", len(voidedItems), txGrandTotal/1000, txGrandTotal))

	voidTx.Commit()
	jsonResponse(w, map[string]interface{}{"status": "ok", "message": "Transaction voided with full reversal"}, 200)
}
func handleGetStats(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006-01-02")
	todayPattern := today + "%"

	var totalSales, totalTx, totalProfit, lowStock, memberCount int
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE created_at LIKE ? AND status='completed'", todayPattern).Scan(&totalSales)
	db.QueryRow("SELECT COUNT(*) FROM transactions WHERE created_at LIKE ? AND status='completed'", todayPattern).Scan(&totalTx)
	db.QueryRow("SELECT COALESCE(SUM(ti.subtotal - p.cost * ti.qty),0) FROM tx_items ti JOIN products p ON ti.product_id=p.id JOIN transactions t ON ti.tx_id=t.tx_id WHERE t.created_at LIKE ? AND t.status='completed'", todayPattern).Scan(&totalProfit)
	db.QueryRow("SELECT COUNT(*) FROM products WHERE active=1 AND stock<10").Scan(&lowStock)
	db.QueryRow("SELECT COUNT(*) FROM members WHERE active=1").Scan(&memberCount)

	type topProd struct {
		Name     string `json:"name"`
		TotalQty int    `json:"total_qty"`
		TotalRev int    `json:"total_rev"`
	}
	tpRows, _ := db.Query("SELECT p.name, SUM(ti.qty), SUM(ti.subtotal) FROM tx_items ti JOIN products p ON ti.product_id=p.id JOIN transactions t ON ti.tx_id=t.tx_id WHERE t.created_at LIKE ? AND t.status='completed' GROUP BY p.name ORDER BY SUM(ti.qty) DESC LIMIT 5", todayPattern)
	defer tpRows.Close()
	var topProducts []topProd
	for tpRows.Next() {
		var tp topProd
		tpRows.Scan(&tp.Name, &tp.TotalQty, &tp.TotalRev)
		topProducts = append(topProducts, tp)
	}

	type recentTx struct {
		TxID       string `json:"tx_id"`
		GrandTotal int    `json:"grand_total"`
		Payment    string `json:"payment"`
		Cashier    string `json:"cashier"`
		CreatedAt  string `json:"created_at"`
	}
	rtRows, _ := db.Query("SELECT tx_id,grand_total,payment,cashier,created_at FROM transactions WHERE status='completed' ORDER BY created_at DESC LIMIT 10")
	defer rtRows.Close()
	var recentTxs []recentTx
	for rtRows.Next() {
		var rt recentTx
		rtRows.Scan(&rt.TxID, &rt.GrandTotal, &rt.Payment, &rt.Cashier, &rt.CreatedAt)
		recentTxs = append(recentTxs, rt)
	}

	aRows, _ := db.Query("SELECT id,shift_name,cashier,opening_cash,total_sales FROM shifts WHERE status='open'")
	defer aRows.Close()
	var activeShifts []Shift
	for aRows.Next() {
		var s Shift
		aRows.Scan(&s.ID, &s.ShiftName, &s.Cashier, &s.OpeningCash, &s.TotalSales)
		activeShifts = append(activeShifts, s)
	}

	jsonResponse(w, map[string]interface{}{
		"total_sales": totalSales, "total_tx": totalTx, "total_profit": totalProfit,
		"low_stock": lowStock, "member_count": memberCount,
		"top_products": topProducts, "recent_tx": recentTxs, "active_shifts": activeShifts,
	}, 200)
}

func handleWSBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonResponse(w, map[string]string{"error": "POST only"}, 405)
		return
	}
	var msg WSMessage
	if err := decodeJSON(w,r, &msg); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}
	wsBroadcast(msg)
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"status": "ok", "service": "pos-server-go", "version": "2.2"}, 200)
}

// === E-Voucher ===
type EVoucherReq struct {
	Type    string `json:"type"`
	Product string `json:"product"`
	Number  string `json:"number"`
	Amount  int    `json:"amount"`
	Cashier string `json:"cashier"`
	ShiftID int    `json:"shift_id"`
}

func handleEVoucher(w http.ResponseWriter, r *http.Request) {
	var req EVoucherReq
	decodeJSON(w,r, &req)

	txID := generateID("EV", 8)
	adminFee := 1500
	total := req.Amount + adminFee

	db.Exec("INSERT INTO transactions (tx_id,shift_id,total,discount,tax,grand_total,payment,amount_paid,change_amount,customer_name,cashier,notes) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
		txID, nullInt(req.ShiftID), total, 0, 0, total, "CASH", total, 0,
		req.Number, req.Cashier, fmt.Sprintf("E-Voucher %s %s", req.Type, req.Product))

	if req.ShiftID > 0 {
		db.Exec("UPDATE shifts SET total_sales=total_sales+?, total_tx=total_tx+1 WHERE id=?", total, req.ShiftID)
	}

	txData := map[string]interface{}{
		"id": txID, "total": total, "grand_total": total,
		"payment": "CASH", "items": []map[string]interface{}{
			{"name": fmt.Sprintf("%s %s", req.Type, req.Product), "qty": 1, "price": req.Amount, "subtotal": req.Amount},
			{"name": "Admin Fee", "qty": 1, "price": adminFee, "subtotal": adminFee},
		},
		"time": time.Now().Format("15:04:05"), "date": time.Now().Format("2006-01-02"),
	}
	wsBroadcast(WSMessage{Type: "new_transaction", Data: txData})
	jsonResponse(w, txData, 200)
}

func handleGetEVouchers(w http.ResponseWriter, r *http.Request) {
	vouchers := []map[string]interface{}{
		{"type": "pulsa", "product": "Telkomsel 5K", "amount": 5000, "category": "Pulsa"},
		{"type": "pulsa", "product": "Telkomsel 10K", "amount": 10000, "category": "Pulsa"},
		{"type": "pulsa", "product": "Telkomsel 25K", "amount": 25000, "category": "Pulsa"},
		{"type": "pulsa", "product": "Indosat 5K", "amount": 5000, "category": "Pulsa"},
		{"type": "pulsa", "product": "Indosat 10K", "amount": 10000, "category": "Pulsa"},
		{"type": "pulsa", "product": "XL 5K", "amount": 5000, "category": "Pulsa"},
		{"type": "pulsa", "product": "XL 10K", "amount": 10000, "category": "Pulsa"},
		{"type": "data", "product": "Telkomsel 1GB", "amount": 15000, "category": "Data"},
		{"type": "data", "product": "Telkomsel 3GB", "amount": 35000, "category": "Data"},
		{"type": "data", "product": "Indosat 1GB", "amount": 10000, "category": "Data"},
		{"type": "data", "product": "XL 1GB", "amount": 12000, "category": "Data"},
		{"type": "pln", "product": "Token 20K", "amount": 20000, "category": "PLN"},
		{"type": "pln", "product": "Token 50K", "amount": 50000, "category": "PLN"},
		{"type": "pln", "product": "Token 100K", "amount": 100000, "category": "PLN"},
		{"type": "pln", "product": "Token 200K", "amount": 200000, "category": "PLN"},
	}
	jsonResponse(w, vouchers, 200)
}

// === Receipt ===
func handleReceipt(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	txID := parts[len(parts)-2]

	var t Transaction
	err := db.QueryRow("SELECT tx_id,total,discount,tax,grand_total,payment,amount_paid,change_amount,customer_name,cashier,created_at FROM transactions WHERE tx_id=?", txID).
		Scan(&t.TxID, &t.Total, &t.Discount, &t.Tax, &t.GrandTotal, &t.Payment, &t.AmountPaid, &t.ChangeAmt, &t.Customer, &t.Cashier, &t.CreatedAt)
	if err != nil {
		jsonResponse(w, map[string]string{"error": "Transaksi tidak ditemukan"}, 404)
		return
	}

	rows, _ := db.Query("SELECT name,qty,price,discount,subtotal FROM tx_items WHERE tx_id=?", txID)
	defer rows.Close()
	var items []TxItem
	for rows.Next() {
		var it TxItem
		rows.Scan(&it.Name, &it.Qty, &it.Price, &it.Discount, &it.Subtotal)
		items = append(items, it)
	}

	storeName := "POS Simulator"
	storeAddr := ""
	db.QueryRow("SELECT value FROM settings WHERE key='store_name'").Scan(&storeName)
	db.QueryRow("SELECT value FROM settings WHERE key='store_address'").Scan(&storeAddr)

	receipt := map[string]interface{}{
		"store_name": storeName, "address": storeAddr,
		"tx_id": t.TxID, "date": t.CreatedAt, "cashier": t.Cashier,
		"items": items, "subtotal": t.Total, "discount": t.Discount,
		"tax": t.Tax, "total": t.GrandTotal, "payment": t.Payment,
		"amount_paid": t.AmountPaid, "change": t.ChangeAmt,
		"footer": "Terima kasih atas kunjungan Anda!",
	}
	jsonResponse(w, receipt, 200)
}

// === Quick Access ===
func handleQuickAccess(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query(`
		SELECT p.id, p.name, p.price, p.promo_price, p.promo_active, p.stock, p.category,
			COALESCE(SUM(ti.qty),0) as total_sold
		FROM products p LEFT JOIN tx_items ti ON p.id = ti.product_id
		WHERE p.active=1 AND p.stock>0
		GROUP BY p.id ORDER BY total_sold DESC LIMIT 12
	`)
	defer rows.Close()
	var products []map[string]interface{}
	for rows.Next() {
		var id, price, promoPrice, promoActive, stock, totalSold int
		var name, category string
		rows.Scan(&id, &name, &price, &promoPrice, &promoActive, &stock, &category, &totalSold)
		effective := price
		if promoActive == 1 && promoPrice > 0 {
			effective = promoPrice
		}
		products = append(products, map[string]interface{}{
			"id": id, "name": name, "price": price, "effective_price": effective,
			"stock": stock, "category": category, "total_sold": totalSold,
		})
	}
	if products == nil {
		products = []map[string]interface{}{}
	}
	jsonResponse(w, products, 200)
}

// === Daily Report ===
func handleDailyReport(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	pattern := date + "%"

	var totalSales, totalTx, cashSales, qrisSales, tfSales, totalProfit, totalDiscount, totalTax int
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0), COUNT(*) FROM transactions WHERE created_at LIKE ? AND status='completed'", pattern).Scan(&totalSales, &totalTx)
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE created_at LIKE ? AND payment='CASH' AND status='completed'", pattern).Scan(&cashSales)
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE created_at LIKE ? AND payment='QRIS' AND status='completed'", pattern).Scan(&qrisSales)
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0) FROM transactions WHERE created_at LIKE ? AND payment='TRANSFER' AND status='completed'", pattern).Scan(&tfSales)
	db.QueryRow("SELECT COALESCE(SUM(ti.subtotal - p.cost * ti.qty),0) FROM tx_items ti JOIN products p ON ti.product_id=p.id JOIN transactions t ON ti.tx_id=t.tx_id WHERE t.created_at LIKE ? AND t.status='completed'", pattern).Scan(&totalProfit)
	db.QueryRow("SELECT COALESCE(SUM(discount),0) FROM transactions WHERE created_at LIKE ? AND status='completed'", pattern).Scan(&totalDiscount)
	db.QueryRow("SELECT COALESCE(SUM(tax),0) FROM transactions WHERE created_at LIKE ? AND status='completed'", pattern).Scan(&totalTax)

	type itemReport struct {
		Name    string `json:"name"`
		Qty     int    `json:"qty"`
		Revenue int    `json:"revenue"`
	}
	irRows, _ := db.Query("SELECT p.name, SUM(ti.qty), SUM(ti.subtotal) FROM tx_items ti JOIN products p ON ti.product_id=p.id JOIN transactions t ON ti.tx_id=t.tx_id WHERE t.created_at LIKE ? AND t.status='completed' GROUP BY p.name ORDER BY SUM(ti.qty) DESC LIMIT 10", pattern)
	defer irRows.Close()
	var topItems []itemReport
	for irRows.Next() {
		var ir itemReport
		irRows.Scan(&ir.Name, &ir.Qty, &ir.Revenue)
		topItems = append(topItems, ir)
	}

	type hourlyReport struct {
		Hour    string `json:"hour"`
		TxCount int    `json:"tx_count"`
		Sales   int    `json:"sales"`
	}
	hrRows, _ := db.Query("SELECT strftime('%H:00', created_at), COUNT(*), SUM(grand_total) FROM transactions WHERE created_at LIKE ? AND status='completed' GROUP BY strftime('%H:00', created_at) ORDER BY strftime('%H:00', created_at)", pattern)
	defer hrRows.Close()
	var hourly []hourlyReport
	for hrRows.Next() {
		var hr hourlyReport
		hrRows.Scan(&hr.Hour, &hr.TxCount, &hr.Sales)
		hourly = append(hourly, hr)
	}

	type lowStockItem struct {
		Name  string `json:"name"`
		Stock int    `json:"stock"`
	}
	lsRows, _ := db.Query("SELECT name, stock FROM products WHERE active=1 AND stock<10 ORDER BY stock ASC")
	defer lsRows.Close()
	var lowStock []lowStockItem
	for lsRows.Next() {
		var ls lowStockItem
		lsRows.Scan(&ls.Name, &ls.Stock)
		lowStock = append(lowStock, ls)
	}

	jsonResponse(w, map[string]interface{}{
		"date": date, "total_sales": totalSales, "total_tx": totalTx,
		"cash_sales": cashSales, "qris_sales": qrisSales, "tf_sales": tfSales,
		"total_profit": totalProfit, "total_discount": totalDiscount, "total_tax": totalTax,
		"top_items": topItems, "hourly": hourly, "low_stock": lowStock,
	}, 200)
}

// === Stock Report ===
func handleStockReport(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query("SELECT id,sku,name,category,stock,cost,price,unit FROM products WHERE active=1 ORDER BY category, name")
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var p Product
		rows.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.Stock, &p.Cost, &p.Price, &p.Unit)
		products = append(products, p)
	}
	jsonResponse(w, products, 200)
}

// === Sales Trend ===
func handleSalesTrend(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query(`
		SELECT DATE(created_at) as date, SUM(grand_total) as total, COUNT(*) as tx_count
		FROM transactions WHERE status='completed' AND created_at >= date('now','-7 days')
		GROUP BY DATE(created_at) ORDER BY date
	`)
	defer rows.Close()
	type TrendPoint struct {
		Date    string `json:"date"`
		Sales   int    `json:"sales"`
		TxCount int    `json:"tx_count"`
	}
	var trend []TrendPoint
	for rows.Next() {
		var tp TrendPoint
		rows.Scan(&tp.Date, &tp.Sales, &tp.TxCount)
		trend = append(trend, tp)
	}
	jsonResponse(w, trend, 200)
}

// === Payment Breakdown ===
func handlePaymentBreakdown(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006-01-02") + "%"
	type PayMethod struct {
		Method string `json:"method"`
		Count  int    `json:"count"`
		Total  int    `json:"total"`
	}
	rows, _ := db.Query("SELECT payment, COUNT(*), SUM(grand_total) FROM transactions WHERE created_at LIKE ? AND status='completed' GROUP BY payment", today)
	defer rows.Close()
	var breakdown []PayMethod
	for rows.Next() {
		var pm PayMethod
		rows.Scan(&pm.Method, &pm.Count, &pm.Total)
		breakdown = append(breakdown, pm)
	}
	jsonResponse(w, breakdown, 200)
}

// === Low Stock Alerts ===
func handleLowStock(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id,sku,name,stock,category FROM products WHERE active=1 AND stock<10 ORDER BY stock ASC")
	if err != nil {
		logError("handleLowStock", err)
		jsonResponse(w, []map[string]interface{}{}, 200)
		return
	}
	defer rows.Close()
	var products []map[string]interface{}
	for rows.Next() {
		var id, stock int
		var sku, name, category string
		rows.Scan(&id, &sku, &name, &stock, &category)
		products = append(products, map[string]interface{}{"id": id, "sku": sku, "name": name, "stock": stock, "category": category})
	}
	if products == nil {
		products = []map[string]interface{}{}
	}
	jsonResponse(w, products, 200)
}

// === Backup / Restore ===
func handleBackup(w http.ResponseWriter, r *http.Request) {
	dbPath := getDataDir() + "/pos.db"
	w.Header().Set("Content-Disposition", "attachment; filename=pos_backup_"+time.Now().Format("20060102_150405")+".db")
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, dbPath)
}

func handleRestore(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(32 << 20) // 32MB max
	file, _, err := r.FormFile("file")
	if err != nil {
		jsonResponse(w, map[string]string{"error": "File tidak ditemukan"}, 400)
		return
	}
	defer file.Close()

	// Validate SQLite header (first 16 bytes must be "SQLite format 3\0")
	header := make([]byte, 16)
	n, err := file.Read(header)
	if err != nil || n < 16 || string(header[:15]) != "SQLite format 3" {
		jsonResponse(w, map[string]string{"error": "File bukan database SQLite yang valid"}, 400)
		return
	}
	// Seek back to start
	file.(io.Seeker).Seek(0, io.SeekStart)

	dbPath := getDataDir() + "/pos.db"
	// Write to temp file first, then rename (atomic)
	tmpPath := dbPath + ".restore_tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		logError("handleRestore create", err)
		jsonResponse(w, map[string]string{"error": "Gagal simpan file"}, 500)
		return
	}
	io.Copy(out, file)
	out.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, dbPath); err != nil {
		os.Remove(tmpPath)
		logError("handleRestore rename", err)
		jsonResponse(w, map[string]string{"error": "Gagal replace database"}, 500)
		return
	}

	jsonResponse(w, map[string]string{"status": "ok", "message": "Database berhasil direstore. Restart server untuk menerapkan."}, 200)
}

// === Settings ===
func handleGetSettings(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query("SELECT key, value FROM settings")
	defer rows.Close()
	settings := map[string]string{}
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		settings[k] = v
	}
	jsonResponse(w, settings, 200)
}

func handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings map[string]string
	if err := decodeJSON(w,r, &settings); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}
	for k, v := range settings {
		db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", k, v)
	}
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

// === AI INTEGRATION ===

// Webhook receiver — AI agent bisa trigger action

// CSRF token getter
func handleGetCSRFToken(w http.ResponseWriter, r *http.Request) {
	// Get session to bind CSRF token to this session
	sessionToken := r.Header.Get("Authorization")
	if sessionToken == "" {
		jsonResponse(w, map[string]string{"error": "Login required"}, 401)
		return
	}
	sessionsMu.RLock()
	sess, exists := sessions[sessionToken]
	sessionsMu.RUnlock()
	if !exists || time.Now().After(sess.expiresAt) {
		jsonResponse(w, map[string]string{"error": "Session expired"}, 401)
		return
	}
	token := generateCSRFToken(sessionToken)
	jsonResponse(w, map[string]string{"csrf_token": token}, 200)
}


func handleRestockCandidates(w http.ResponseWriter, r *http.Request) {
	var threshold int = 10
	thresholdStr := r.URL.Query().Get("threshold")
	if thresholdStr != "" {
		threshold, _ = strconv.Atoi(thresholdStr)
	}
	var aiThreshold string
	db.QueryRow("SELECT value FROM settings WHERE key='ai_stock_threshold'").Scan(&aiThreshold)
	if aiThreshold != "" {
		t, _ := strconv.Atoi(aiThreshold)
		if t > 0 { threshold = t }
	}
	rows, _ := db.Query("SELECT id,sku,name,stock,category,price,cost FROM products WHERE active=1 AND stock<? ORDER BY stock", threshold)
	defer rows.Close()
	var candidates []map[string]interface{}
	for rows.Next() {
		var id, stock, price, cost int
		var sku, name, category string
		rows.Scan(&id, &sku, &name, &stock, &category, &price, &cost)
		margin := 0
		if cost > 0 { margin = ((price - cost) * 100) / cost }
		candidates = append(candidates, map[string]interface{}{
			"product_id": id, "sku": sku, "name": name,
			"stock": stock, "category": category,
			"price": price, "cost": cost, "margin_pct": margin,
		})
	}
	jsonResponse(w, map[string]interface{}{
		"version": "1.0",
		"threshold": threshold,
		"candidates": candidates,
		"count": len(candidates),
	}, 200)
}

func handleAIWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonResponse(w, map[string]string{"error": "POST only"}, 405)
		return
	}
	// Validate AI webhook secret
	var aiSecret string
	db.QueryRow("SELECT value FROM settings WHERE key='ai_webhook_secret'").Scan(&aiSecret)
	if aiSecret != "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+aiSecret {
			auditLog("webhook_rejected", "ai", "", "", "Invalid secret")
			jsonResponse(w, map[string]string{"error": "Unauthorized"}, 401)
			return
		}
	}
	var req struct {
		Action string      `json:"action"`
		Data   interface{} `json:"data"`
		RequestID string    `json:"request_id"`
	}
	if err := decodeJSON(w,r, &req); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}

	switch req.Action {
	case "stock_adjustment":
		data, _ := json.Marshal(req.Data)
		var adj struct {
			ProductID    int    `json:"product_id"`
			Operation    string `json:"operation"`     // set, increase, decrease
			Quantity     int    `json:"quantity"`
			ExpectedStock int   `json:"expected_stock"`
			Source       string `json:"source"`        // ai, manual, supplier_receipt, stock_count
			Reason       string `json:"reason"`
		}
		json.Unmarshal(data, &adj)
		if adj.ProductID == 0 || adj.Operation == "" || adj.Quantity < 0 {
			jsonResponse(w, map[string]string{"error": "product_id, operation, and quantity required"}, 400)
			return
		}
		if adj.Operation != "set" && adj.Operation != "increase" && adj.Operation != "decrease" {
			jsonResponse(w, map[string]string{"error": "operation must be: set, increase, decrease"}, 400)
			return
		}
		// Check ai_mode
		var aiMode string
		db.QueryRow("SELECT value FROM settings WHERE key='ai_mode'").Scan(&aiMode)
		if aiMode == "suggest_only" {
			auditLog("ai_suggestion", "product", fmt.Sprintf("%d", adj.ProductID), "AI_AGENT",
				fmt.Sprintf("Suggested %s %d (suggest_only mode)", adj.Operation, adj.Quantity))
			jsonResponse(w, map[string]interface{}{"status": "ok", "applied": false, "message": "Suggestion logged (suggest_only mode)"}, 200)
			return
		}
		// Check max daily
		var maxDaily int
		maxDailyStr := ""
		db.QueryRow("SELECT value FROM settings WHERE key='ai_max_daily_updates'").Scan(&maxDailyStr)
		if maxDailyStr != "" { maxDaily, _ = strconv.Atoi(maxDailyStr) }
		if maxDaily > 0 {
			var todayCount int
			db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='ai_stock_adjustment' AND DATE(created_at)=DATE('now')").Scan(&todayCount)
			if todayCount >= maxDaily {
				jsonResponse(w, map[string]interface{}{"status": "error", "applied": false, "message": fmt.Sprintf("Daily limit reached (%d/%d)", todayCount, maxDaily)}, 429)
				return
			}
		}
		// Idempotency table
		if req.RequestID != "" {
			var exists int
			db.QueryRow("SELECT COUNT(*) FROM idempotency_keys WHERE key=?", req.RequestID).Scan(&exists)
			if exists > 0 {
				jsonResponse(w, map[string]interface{}{"status": "ok", "applied": false, "message": "Already processed (idempotent)"}, 200)
				return
			}
		}
		// Optimistic concurrency
		var currentStock int
		err := db.QueryRow("SELECT stock FROM products WHERE id=?", adj.ProductID).Scan(&currentStock)
		if err != nil {
			jsonResponse(w, map[string]string{"error": "Product not found"}, 404)
			return
		}
		if adj.ExpectedStock > 0 && currentStock != adj.ExpectedStock {
			jsonResponse(w, map[string]interface{}{"status": "error", "applied": false,
				"message": fmt.Sprintf("Stock mismatch: expected %d, actual %d", adj.ExpectedStock, currentStock)}, 409)
			return
		}
		var newStock int
		switch adj.Operation {
		case "set":
			newStock = adj.Quantity
		case "increase":
			newStock = currentStock + adj.Quantity
		case "decrease":
			newStock = currentStock - adj.Quantity
			if newStock < 0 { newStock = 0 }
		}
		// Atomic: idempotency + stock update in same tx
		adjTx, _ := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelDefault})
		defer adjTx.Rollback()
		adjTx.Exec("UPDATE products SET stock=? WHERE id=?", newStock, adj.ProductID)
		adjTx.Exec("INSERT INTO inventory_movements (product_id,movement_type,quantity,stock_before,stock_after,reference_type,source,reason,user) VALUES (?,?,?,?,?,?,?,?,?)",
			adj.ProductID, adj.Operation, adj.Quantity, currentStock, newStock, "ai_adjustment", adj.Source, adj.Reason, "AI_AGENT")
		// Audit
		auditLog("ai_stock_adjustment", "product", fmt.Sprintf("%d", adj.ProductID), "AI_AGENT",
			fmt.Sprintf("%s %d (before=%d after=%d) source=%s reason=%s request_id=%s",
				adj.Operation, adj.Quantity, currentStock, newStock, adj.Source, adj.Reason, req.RequestID))
		// Save idempotency key
		if req.RequestID != "" {
			resp, _ := json.Marshal(map[string]interface{}{"status": "ok", "applied": true})
			adjTx.Exec("INSERT INTO idempotency_keys (key,action,response_json,expires_at) VALUES (?,?,?,datetime('now'))",
				req.RequestID, "stock_adjustment", string(resp))
		}
		adjTx.Commit()
		jsonResponse(w, map[string]interface{}{"status": "ok", "applied": true, "message": "Stock adjusted"}, 200)

	case "restock_recommendation":
		// AI agent baca stok rendah
		rows, _ := db.Query("SELECT id,sku,name,stock,category FROM products WHERE active=1 AND stock<10 ORDER BY stock")
		defer rows.Close()
		var recommendations []map[string]interface{}
		for rows.Next() {
			var id, stock int
			var sku, name, category string
			rows.Scan(&id, &sku, &name, &stock, &category)
			recommendations = append(recommendations, map[string]interface{}{
				"product_id": id, "sku": sku, "name": name, "stock": stock, "category": category,
			})
		}
		jsonResponse(w, recommendations, 200)

	default:
		jsonResponse(w, map[string]string{"error": "Unknown action"}, 400)
	}
}

// Daily report — AI agent bisa baca untuk analisis
func handleAIReport(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	// Total sales
	var totalSales, totalTx, totalTax int
	db.QueryRow("SELECT COALESCE(SUM(grand_total),0), COUNT(*), COALESCE(SUM(tax),0) FROM transactions WHERE status='completed' AND DATE(created_at)=?", date).Scan(&totalSales, &totalTx, &totalTax)

	// Sales per product
	rows, _ := db.Query(`
		SELECT ti.name, SUM(ti.qty) as total_qty, SUM(ti.subtotal) as total_revenue
		FROM tx_items ti
		JOIN transactions t ON ti.tx_id = t.tx_id
		WHERE t.status='completed' AND DATE(t.created_at)=?
		GROUP BY ti.name ORDER BY total_revenue DESC
	`, date)
	defer rows.Close()
	type ProductSales struct {
		Name    string `json:"name"`
		Qty     int    `json:"qty"`
		Revenue int    `json:"revenue"`
	}
	var productSales []ProductSales
	for rows.Next() {
		var ps ProductSales
		rows.Scan(&ps.Name, &ps.Qty, &ps.Revenue)
		productSales = append(productSales, ps)
	}

	// Low stock items
	lowStockRows, _ := db.Query("SELECT id,sku,name,stock,category FROM products WHERE active=1 AND stock<10 ORDER BY stock")
	defer lowStockRows.Close()
	var lowStock []map[string]interface{}
	for lowStockRows.Next() {
		var id, stock int
		var sku, name, category string
		lowStockRows.Scan(&id, &sku, &name, &stock, &category)
		lowStock = append(lowStock, map[string]interface{}{
			"product_id": id, "sku": sku, "name": name, "stock": stock, "category": category,
		})
	}

	// Member activity
	memberRows, _ := db.Query(`
		SELECT m.name, m.member_id, COUNT(t.id) as tx_count, SUM(t.grand_total) as total_spent
		FROM transactions t
		JOIN members m ON t.member_id = m.id
		WHERE t.status='completed' AND DATE(t.created_at)=?
		GROUP BY m.id ORDER BY total_spent DESC LIMIT 10
	`, date)
	defer memberRows.Close()
	type MemberActivity struct {
		Name       string `json:"name"`
		MemberID   string `json:"member_id"`
		TxCount    int    `json:"tx_count"`
		TotalSpent int    `json:"total_spent"`
	}
	var memberActivity []MemberActivity
	for memberRows.Next() {
		var ma MemberActivity
		memberRows.Scan(&ma.Name, &ma.MemberID, &ma.TxCount, &ma.TotalSpent)
		memberActivity = append(memberActivity, ma)
	}

	jsonResponse(w, map[string]interface{}{
		"date":            date,
		"total_sales":     totalSales,
		"total_tx":        totalTx,
		"total_tax":       totalTax,
		"product_sales":   productSales,
		"low_stock":       lowStock,
		"member_activity": memberActivity,
	}, 200)
}

// Settings for AI integration
func handleGetAISettings(w http.ResponseWriter, r *http.Request) {
	var settings map[string]string
	settings = make(map[string]string)
	rows, _ := db.Query("SELECT key, value FROM settings WHERE key LIKE 'ai_%'")
	defer rows.Close()
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		// Mask secret — never return actual value
		if k == "ai_webhook_secret" {
			if v != "" {
				settings[k] = "****" + v[len(v)-4:]
			} else {
				settings[k] = ""
			}
		} else {
			settings[k] = v
		}
	}
	jsonResponse(w, settings, 200)
}

func handleUpdateAISettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := decodeJSON(w,r, &req); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid"}, 400)
		return
	}
	for k, v := range req {
		if len(k) > 3 && k[:3] == "ai_" {
			db.Exec("INSERT OR REPLACE INTO settings (key,value) VALUES (?,?)", k, v)
		}
	}
	jsonResponse(w, map[string]string{"status": "ok"}, 200)
}

// === SESSION MIDDLEWARE ===
func getSessionUser(r *http.Request) (string, string) {
	token := r.Header.Get("Authorization")
	if token == "" {
		return "", ""
	}
	sessionsMu.RLock()
	sess, exists := sessions[token]
	sessionsMu.RUnlock()
	if !exists || time.Now().After(sess.expiresAt) {
		return "", ""
	}
	return token, sess.role
}

// === CHANGE PASSWORD ===
func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	token, _ := getSessionUser(r)
	if token == "" {
		jsonResponse(w, map[string]string{"error": "Login required"}, 401)
		return
	}
	var req struct {
		Username    string `json:"username"`
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(w,r, &req); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}
	if len(req.NewPassword) < 6 {
		jsonResponse(w, map[string]string{"error": "Password minimal 6 karakter"}, 400)
		return
	}
	var currentHash string
	err := db.QueryRow("SELECT password FROM users WHERE username=?", req.Username).Scan(&currentHash)
	if err != nil {
		jsonResponse(w, map[string]string{"error": "User not found"}, 404)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.OldPassword)); err != nil {
		jsonResponse(w, map[string]string{"error": "Password lama salah"}, 401)
		return
	}
	newHash, _ := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 10)
	db.Exec("UPDATE users SET password=?, password_changed=1 WHERE username=?", string(newHash), req.Username)
	auditLog("password_change", "user", req.Username, req.Username, "Password changed")
	jsonResponse(w, map[string]interface{}{"status": "ok", "message": "Password berhasil diubah"}, 200)
}


// === MEMBER TRANSACTION HISTORY ===
func handleMemberTransactions(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	memberID := parts[len(parts)-2]

	rows, err := db.Query(`SELECT t.tx_id, t.total, t.discount, t.tax, t.grand_total, t.payment, t.created_at
		FROM transactions t
		JOIN members m ON t.member_id = m.id
		WHERE m.member_id = ? AND t.status = 'completed'
		ORDER BY t.created_at DESC LIMIT 50`, memberID)
	if err != nil {
		logError("handleMemberTransactions", err)
		jsonResponse(w, map[string]string{"error": "Database error"}, 500)
		return
	}
	defer rows.Close()
	var txs []map[string]interface{}
	for rows.Next() {
		var txID, payment, createdAt string
		var total, discount, tax, grandTotal int
		rows.Scan(&txID, &total, &discount, &tax, &grandTotal, &payment, &createdAt)
		txs = append(txs, map[string]interface{}{
			"tx_id": txID, "total": total, "discount": discount,
			"tax": tax, "grand_total": grandTotal,
			"payment": payment, "created_at": createdAt,
		})
	}
	if txs == nil {
		txs = []map[string]interface{}{}
	}
	jsonResponse(w, txs, 200)
}

// === STOCK ADJUSTMENT ===
func handleStockAdjustment(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonResponse(w, map[string]string{"error": "POST only"}, 405)
		return
	}
	var req struct {
		ProductID  int    `json:"product_id"`
		Quantity   int    `json:"quantity"`
		Type       string `json:"type"` // "in", "out", "adjust"
		Reason     string `json:"reason"`
		ShiftID    int    `json:"shift_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		jsonResponse(w, map[string]string{"error": "Invalid request"}, 400)
		return
	}
	if req.ProductID == 0 || req.Quantity <= 0 || req.Reason == "" {
		jsonResponse(w, map[string]string{"error": "product_id, quantity (>0), and reason required"}, 400)
		return
	}

	var currentStock int
	err := db.QueryRow("SELECT stock FROM products WHERE id=? AND active=1", req.ProductID).Scan(&currentStock)
	if err != nil {
		jsonResponse(w, map[string]string{"error": "Product not found"}, 404)
		return
	}

	var newStock int
	var movementType string
	switch req.Type {
	case "in":
		newStock = currentStock + req.Quantity
		movementType = "stock_in"
	case "out":
		if currentStock < req.Quantity {
			jsonResponse(w, map[string]string{"error": "Insufficient stock"}, 400)
			return
		}
		newStock = currentStock - req.Quantity
		movementType = "stock_out"
	case "adjust":
		newStock = req.Quantity
		movementType = "adjustment"
	default:
		jsonResponse(w, map[string]string{"error": "type must be: in, out, adjust"}, 400)
		return
	}

	db.Exec("UPDATE products SET stock=? WHERE id=?", newStock, req.ProductID)
	db.Exec("INSERT INTO inventory_movements (product_id,movement_type,quantity,stock_before,stock_after,reference_type,source,reason,user) VALUES (?,?,?,?,?,?,?,?,?)",
		req.ProductID, movementType, req.Quantity, currentStock, newStock, "manual", "admin", req.Reason, "admin")

	auditLog("stock_adjustment", "product", fmt.Sprintf("%d", req.ProductID), "admin",
		fmt.Sprintf("%s %d (before=%d after=%d) reason=%s", movementType, req.Quantity, currentStock, newStock, req.Reason))

	jsonResponse(w, map[string]interface{}{"status": "ok", "product_id": req.ProductID, "before": currentStock, "after": newStock, "type": movementType}, 200)
}

// === DISPLAY TOKEN ===
var displayTokens = struct {
	sync.RWMutex
	data map[string]time.Time
}{data: make(map[string]time.Time)}

func generateDisplayToken() string {
	token := generateID("DISP", 8)
	displayTokens.Lock()
	displayTokens.data[token] = time.Now().Add(24 * time.Hour)
	displayTokens.Unlock()
	return token
}

func validateDisplayToken(token string) bool {
	if token == "" { return false }
	displayTokens.RLock()
	exp, ok := displayTokens.data[token]
	displayTokens.RUnlock()
	if !ok { return false }
	if time.Now().After(exp) {
		displayTokens.Lock()
		delete(displayTokens.data, token)
		displayTokens.Unlock()
		return false
	}
	return true
}

func handleGenerateDisplayToken(w http.ResponseWriter, r *http.Request) {
	token := generateDisplayToken()
	jsonResponse(w, map[string]interface{}{"token": token, "expires_in": 86400}, 200)
}
