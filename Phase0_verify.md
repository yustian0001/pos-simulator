# POS Simulator — Phase 0 Verification Files

**Date:** 2026-08-31 | **Status:** Phase 0 implemented

---

## main.go

```
1|package main
2|
3|import (
4|	"crypto/rand"
5|	"database/sql"
6|	"encoding/hex"
7|	"encoding/json"
8|	"fmt"
9|	"log"
10|	"os"
11|	"path/filepath"
12|	"sync"
13|	"time"
14|
15|	_ "modernc.org/sqlite"
16|	_ "github.com/tursodatabase/libsql-client-go/libsql"
17|	"golang.org/x/crypto/bcrypt"
18|)
19|
20|var db *sql.DB
21|
22|// Session store with expiry
23|type session struct {
24|	role      string
25|	username  string
26|	expiresAt time.Time
27|}
28|
29|var (
30|	sessions   = make(map[string]*session)
31|	sessionsMu sync.RWMutex
32|)
33|
34|func createSession(role string, username string) string {
35|	b := make([]byte, 32)
36|	rand.Read(b)
37|	token := hex.EncodeToString(b)
38|	sessionsMu.Lock()
39|	sessions[token] = &session{
40|		role:      role,
41|		username:  username,
42|		expiresAt: time.Now().Add(8 * time.Hour), // 8 jam per shift
43|	}
44|	sessionsMu.Unlock()
45|	return token
46|}
47|
48|func validateSession(token string) (string, bool) {
49|	sessionsMu.RLock()
50|	defer sessionsMu.RUnlock()
51|	s, ok := sessions[token]
52|	if !ok {
53|		return "", false
54|	}
55|	if time.Now().After(s.expiresAt) {
56|		delete(sessions, token)
57|		return "", false
58|	}
59|	return s.role, true
60|}
61|
62|func deleteSession(token string) {
63|	sessionsMu.Lock()
64|	delete(sessions, token)
65|	sessionsMu.Unlock()
66|}
67|
68|func cleanupSessions() {
69|	for {
70|		time.Sleep(5 * time.Minute)
71|		sessionsMu.Lock()
72|		now := time.Now()
73|		for token, s := range sessions {
74|			if now.After(s.expiresAt) {
75|				delete(sessions, token)
76|			}
77|		}
78|		sessionsMu.Unlock()
79|	}
80|}
81|
82|// Generate secure random ID
83|func generateID(prefix string, length int) string {
84|	b := make([]byte, length)
85|	rand.Read(b)
86|	return fmt.Sprintf("%s%s", prefix, hex.EncodeToString(b)[:length*2])
87|}
88|
89|type Product struct {
90|	ID          int     `json:"id"`
91|	SKU         string  `json:"sku"`
92|	Name        string  `json:"name"`
93|	Price       int     `json:"price"`
94|	Cost        int     `json:"cost,omitempty"`
95|	Category    string  `json:"category"`
96|	Stock       int     `json:"stock"`
97|	Unit        string  `json:"unit"`
98|	Barcode     string  `json:"barcode"`
99|	PromoPrice  int     `json:"promo_price"`
100|	PromoActive int     `json:"promo_active"`
101|	TaxRate     float64 `json:"tax_rate"`
102|	Active      int     `json:"active"`
103|}
104|
105|type Transaction struct {
106|	ID         int     `json:"id"`
107|	TxID       string  `json:"tx_id"`
108|	ShiftID    *int    `json:"shift_id"`
109|	Total      int     `json:"total"`
110|	Discount   int     `json:"discount"`
111|	Tax        int     `json:"tax"`
112|	GrandTotal int     `json:"grand_total"`
113|	Payment    string  `json:"payment"`
114|	AmountPaid int     `json:"amount_paid"`
115|	ChangeAmt  int     `json:"change_amount"`
116|	Customer   string  `json:"customer_name"`
117|	MemberID   *string `json:"member_id"`
118|	Cashier    string  `json:"cashier"`
119|	Notes      string  `json:"notes"`
120|	Status     string  `json:"status"`
121|	CreatedAt  string  `json:"created_at"`
122|}
123|
124|type TxItem struct {
125|	ID        int    `json:"id"`
126|	TxID      string `json:"tx_id"`
127|	ProductID int    `json:"product_id"`
128|	Name      string `json:"name"`
129|	Qty       int    `json:"qty"`
130|	Price     int    `json:"price"`
131|	Discount  int    `json:"discount"`
132|	Subtotal  int    `json:"subtotal"`
133|	Notes     string `json:"notes"`
134|}
135|
136|type Shift struct {
137|	ID           int     `json:"id"`
138|	ShiftName    string  `json:"shift_name"`
139|	Cashier      string  `json:"cashier"`
140|	OpenedAt     string  `json:"opened_at"`
141|	ClosedAt     *string `json:"closed_at"`
142|	OpeningCash  int     `json:"opening_cash"`
143|	ClosingCash  int     `json:"closing_cash"`
144|	ExpectedCash int     `json:"expected_cash"`
145|	CashSales    int     `json:"cash_sales"`
146|	CashOut      int     `json:"cash_out"`
147|	Discrepancy  int     `json:"cash_discrepancy"`
148|	TotalSales   int     `json:"total_sales"`
149|	TotalTx      int     `json:"total_tx"`
150|	Status       string  `json:"status"`
151|}
152|
153|type CashLog struct {
154|	ID          int    `json:"id"`
155|	ShiftID     int    `json:"shift_id"`
156|	Type        string `json:"type"`
157|	Amount      int    `json:"amount"`
158|	Description string `json:"description"`
159|	CreatedAt   string `json:"created_at"`
160|}
161|
162|type Member struct {
163|	ID       int    `json:"id"`
164|	MemberID string `json:"member_id"`
165|	Name     string `json:"name"`
166|	Phone    string `json:"phone"`
167|	Email    string `json:"email"`
168|	Points   int    `json:"points"`
169|	Tier     string `json:"tier"`
170|	Active   int    `json:"active"`
171|}
172|
173|type User struct {
174|	ID          int    `json:"id"`
175|	Username    string `json:"username"`
176|	Password    string `json:"-"`
177|	DisplayName string `json:"display_name"`
178|	Role        string `json:"role"`
179|	Active      int    `json:"active"`
180|}
181|
182|type CartItemReq struct {
183|	ProductID int    `json:"product_id"`
184|	Qty       int    `json:"qty"`
185|	Notes     string `json:"notes"`
186|	Discount  int    `json:"discount"`
187|}
188|
189|type CheckoutReq struct {
190|	Items        []CartItemReq `json:"items"`
191|	Payment      string        `json:"payment"`
192|	Discount     int           `json:"discount"`
193|	AmountPaid   int           `json:"amount_paid"`
194|	CustomerName string        `json:"customer_name"`
195|	MemberID     string        `json:"member_id"`
196|	Cashier      string        `json:"cashier"`
197|	ShiftID      int           `json:"shift_id"`
198|	Notes        string        `json:"notes"`
199|}
200|
201|type HoldReq struct {
202|	Items        json.RawMessage `json:"items"`
203|	CustomerName string          `json:"customer_name"`
204|}
205|
206|func getDataDir() string {
207|	exe, err := os.Executable()
208|	if err != nil {
209|		return "."
210|	}
211|	return filepath.Dir(exe)
212|}
213|
214|func initDB() {
215|	// Read config from env vars ONLY (no embedded secrets)
216|	tursoURL := os.Getenv("TURSO_DATABASE_URL")
217|	tursoToken := os.Getenv("TURSO_AUTH_TOKEN")
218|	// Environment variables ONLY - no embedded or file config
219|	// Set TURSO_DATABASE_URL and TURSO_AUTH_TOKEN as env vars
220|	if v := os.Getenv("TURSO_DATABASE_URL"); v != "" {
221|		tursoURL = v
222|	}
223|	if v := os.Getenv("TURSO_AUTH_TOKEN"); v != "" {
224|		tursoToken = v
225|	}
226|
227|	if tursoURL != "" && tursoToken != "" {
228|		dir := getDataDir()
229|		dbPath := filepath.Join(dir, "pos_replica.db")
230|		connStr := fmt.Sprintf("file:%s?syncUrl=%s&authToken=%s&syncInterval=60s", dbPath, tursoURL, tursoToken)
231|		var err error
232|		db, err = sql.Open("libsql", connStr)
233|		if err != nil {
234|			fmt.Printf("[POS] Turso Embedded Replica failed: %v, using local sqlite\n", err)
235|			db = nil
236|		} else {
237|			if pingErr := db.Ping(); pingErr != nil {
238|				fmt.Printf("[POS] Turso Embedded Replica ping warning: %v (offline mode on %s)\n", pingErr, dbPath)
239|			} else {
240|				fmt.Printf("[POS] DB: Turso Embedded Replica active (%s <-> %s)\n", dbPath, tursoURL)
241|			}
242|		}
243|	}
244|
245|	// Fallback to local SQLite
246|	if db == nil {
247|		dir := getDataDir()
248|		dbPath := filepath.Join(dir, "pos.db")
249|		var err error
250|		db, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
251|		if err != nil {
252|			log.Fatal(err)
253|		}
254|		db.Exec("PRAGMA foreign_keys = ON")
255|		fmt.Printf("[POS] DB: %s\n", dbPath)
256|	}
257|
258|	tables := `
259|	CREATE TABLE IF NOT EXISTS products (
260|		id INTEGER PRIMARY KEY AUTOINCREMENT,
261|		sku TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
262|		price INTEGER DEFAULT 0, cost INTEGER DEFAULT 0,
263|		category TEXT DEFAULT 'Umum', stock INTEGER DEFAULT 0,
264|		unit TEXT DEFAULT 'pcs', barcode TEXT DEFAULT '',
265|		promo_price INTEGER DEFAULT 0, promo_active INTEGER DEFAULT 0,
266|		tax_rate REAL DEFAULT -1,
267|		active INTEGER DEFAULT 1, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
268|	);
269|	CREATE TABLE IF NOT EXISTS categories (
270|		id INTEGER PRIMARY KEY AUTOINCREMENT,
271|		name TEXT UNIQUE NOT NULL, icon TEXT DEFAULT '📦'
272|	);
273|	CREATE TABLE IF NOT EXISTS transactions (
274|		id INTEGER PRIMARY KEY AUTOINCREMENT,
275|		tx_id TEXT UNIQUE NOT NULL, shift_id INTEGER,
276|		total INTEGER DEFAULT 0, discount INTEGER DEFAULT 0,
277|		tax INTEGER DEFAULT 0, grand_total INTEGER DEFAULT 0,
278|		payment TEXT DEFAULT 'CASH', amount_paid INTEGER DEFAULT 0,
279|		change_amount INTEGER DEFAULT 0, customer_name TEXT DEFAULT '',
280|		member_id INTEGER DEFAULT NULL, cashier TEXT DEFAULT 'kasir',
281|		notes TEXT DEFAULT '', status TEXT DEFAULT 'completed',
282|		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
283|	);
284|	CREATE TABLE IF NOT EXISTS tx_items (
285|		id INTEGER PRIMARY KEY AUTOINCREMENT,
286|		tx_id TEXT NOT NULL, product_id INTEGER,
287|		name TEXT NOT NULL, qty INTEGER DEFAULT 1,
288|		price INTEGER DEFAULT 0, discount INTEGER DEFAULT 0,
289|		subtotal INTEGER DEFAULT 0, notes TEXT DEFAULT ''
290|	);
291|	CREATE TABLE IF NOT EXISTS users (
292|		id INTEGER PRIMARY KEY AUTOINCREMENT,
293|		username TEXT UNIQUE NOT NULL, password TEXT NOT NULL,
294|		display_name TEXT DEFAULT '', role TEXT DEFAULT 'kasir',
295|		active INTEGER DEFAULT 1
296|	);
297|	CREATE TABLE IF NOT EXISTS shifts (
298|		id INTEGER PRIMARY KEY AUTOINCREMENT,
299|		shift_name TEXT NOT NULL, cashier TEXT NOT NULL,
300|		opened_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
301|		closed_at TIMESTAMP DEFAULT NULL,
302|		opening_cash INTEGER DEFAULT 0, closing_cash INTEGER DEFAULT 0,
303|		expected_cash INTEGER DEFAULT 0, cash_sales INTEGER DEFAULT 0,
304|		cash_out INTEGER DEFAULT 0, cash_discrepancy INTEGER DEFAULT 0,
305|		total_sales INTEGER DEFAULT 0, total_tx INTEGER DEFAULT 0,
306|		status TEXT DEFAULT 'open'
307|	);
308|	CREATE TABLE IF NOT EXISTS cash_log (
309|		id INTEGER PRIMARY KEY AUTOINCREMENT,
310|		shift_id INTEGER, type TEXT NOT NULL,
311|		amount INTEGER DEFAULT 0, description TEXT DEFAULT '',
312|		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
313|	);
314|	CREATE TABLE IF NOT EXISTS members (
315|		id INTEGER PRIMARY KEY AUTOINCREMENT,
316|		member_id TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
317|		phone TEXT DEFAULT '', email TEXT DEFAULT '',
318|		points INTEGER DEFAULT 0, tier TEXT DEFAULT 'basic',
319|		active INTEGER DEFAULT 1, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
320|	);
321|	CREATE TABLE IF NOT EXISTS holds (
322|		id INTEGER PRIMARY KEY AUTOINCREMENT,
323|		hold_id TEXT UNIQUE NOT NULL, items_json TEXT NOT NULL,
324|		customer_name TEXT DEFAULT '',
325|		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
326|	);
327|	CREATE TABLE IF NOT EXISTS settings (
328|		key TEXT PRIMARY KEY, value TEXT NOT NULL
329|	);`
330|	db.Exec(tables)
331|
332|	// Create inventory_movements table
333|	db.Exec(`CREATE TABLE IF NOT EXISTS inventory_movements (
334|		id INTEGER PRIMARY KEY AUTOINCREMENT,
335|		product_id INTEGER NOT NULL,
336|		movement_type TEXT NOT NULL,
337|		quantity INTEGER NOT NULL,
338|		stock_before INTEGER NOT NULL,
339|		stock_after INTEGER NOT NULL,
340|		reference_type TEXT DEFAULT '',
341|		reference_id TEXT DEFAULT '',
342|		source TEXT NOT NULL DEFAULT 'manual',
343|		reason TEXT DEFAULT '',
344|		user TEXT DEFAULT '',
345|		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
346|		FOREIGN KEY (product_id) REFERENCES products(id)
347|	)`)
348|
349|	// Create idempotency_keys table
350|	db.Exec(`CREATE TABLE IF NOT EXISTS idempotency_keys (
351|		key TEXT PRIMARY KEY,
352|		action TEXT NOT NULL,
353|		response_json TEXT NOT NULL,
354|		payload_hash TEXT NOT NULL DEFAULT '',
355|		status_code INTEGER DEFAULT 200,
356|		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
357|		expires_at TIMESTAMP NOT NULL
358|	)`)
359|
360|	// Schema migrations
361|	db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
362|		version INTEGER PRIMARY KEY,
363|		name TEXT NOT NULL,
364|		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
365|		checksum TEXT DEFAULT ''
366|	)`)
367|
368|	// Migration: add columns that may not exist in older DBs
369|	db.Exec("ALTER TABLE users ADD COLUMN password_changed INTEGER DEFAULT 0")
370|	db.Exec("ALTER TABLE products ADD COLUMN tax_rate REAL DEFAULT -1")
371|
372|	db.Exec("INSERT OR IGNORE INTO schema_migrations (version,name,checksum) VALUES (1,'initial','v2.2')")
373|
374|	// Seed data
375|	cats := [][2]string{{"Makanan", "🍜"}, {"Minuman", "🥤"}, {"Snack", "🍿"}, {"Lainnya", "📦"}}
376|	for _, c := range cats {
377|		db.Exec("INSERT OR IGNORE INTO categories (name, icon) VALUES (?, ?)", c[0], c[1])
378|	}
379|
380|	products := []struct {
381|		sku, name, cat, unit, barcode string
382|		price, cost, stock, promoPrice, promoActive int
383|		taxRate float64
384|	}{
385|		{"PRD001", "Nasi Goreng", "Makanan", "pcs", "899001", 25000, 15000, 50, 22000, 1, -1},
386|		{"PRD002", "Ayam Geprek", "Makanan", "pcs", "899002", 35000, 20000, 30, 0, 0, -1},
387|		{"PRD003", "Es Teh", "Minuman", "pcs", "899003", 5000, 2000, 100, 0, 0, 0},
388|		{"PRD004", "Nasi Uduk", "Makanan", "pcs", "899004", 25000, 15000, 40, 0, 0, -1},
389|		{"PRD005", "Jus Alpukat", "Minuman", "pcs", "899005", 15000, 8000, 25, 0, 0, -1},
390|		{"PRD006", "Indomie Goreng", "Makanan", "pcs", "899006", 8000, 5000, 80, 0, 0, -1},
391|		{"PRD007", "Kopi Susu", "Minuman", "pcs", "899007", 18000, 10000, 60, 0, 0, -1},
392|		{"PRD008", "Keripik Singkong", "Snack", "pcs", "899008", 10000, 5000, 45, 0, 0, -1},
393|	}
394|	for _, p := range products {
395|		db.Exec("INSERT OR IGNORE INTO products (sku,name,price,cost,category,stock,unit,barcode,promo_price,promo_active,tax_rate) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
396|			p.sku, p.name, p.price, p.cost, p.cat, p.stock, p.unit, p.barcode, p.promoPrice, p.promoActive, p.taxRate)
397|	}
398|
399|	// Seed users with bcrypt-hashed passwords
400|	users := []struct{ u, p, d, r string }{
401|		{"admin", "admin123", "Admin Utama", "admin"},
402|		{"kasir1", "kasir123", "Andi", "kasir"},
403|		{"kasir2", "kasir123", "Budi", "kasir"},
404|	}
405|	for _, u := range users {
406|		hash, _ := bcrypt.GenerateFromPassword([]byte(u.p), 10)
407|		db.Exec("INSERT OR IGNORE INTO users (username,password,display_name,role) VALUES (?,?,?,?)", u.u, string(hash), u.d, u.r)
408|	}
409|
410|	settings := map[string]string{
411|		"store_name":   "Masjid Jami' Baiturrahman",
412|		"opening_cash": "500000",
413|		"store_address": "Jl. Tole Iskandar No.KM. 3, Mekar Jaya, Kec. Sukmajaya, Kota Depok, Jawa Barat 16411",
414|		"store_phone":  "081234567890",
415|		"ad_title":     "Promo Spesial Hari Ini!",
416|		"ad_desc":      "Dapatkan diskon menarik untuk semua produk pilihan",
417|		"ad_marquee":   "🎉 Promo 17 Agustus! Diskon 17% semua makanan! 🎉 Gratis Es Teh untuk pembelian di atas Rp 50.000! 🎉",
418|		"ad_cards":     `[{"emoji":"🍜","name":"Nasi Goreng","price":22000,"old_price":25000},{"emoji":"🥤","name":"Es Teh","price":5000,"old_price":null}]`,
419|		"qris_merchant": "POS Simulator",
420|		"qris_amount":   "0",
421|		"ppn_rate":      "11",
422|	}
423|	for k, v := range settings {
424|		db.Exec("INSERT OR IGNORE INTO settings (key,value) VALUES (?,?)", k, v)
425|	}
426|
427|	// Seed 5 test members
428|	members := []struct{ mid, name, phone, email, tier string; points int }{
429|		{"MEM000001", "Budi Santoso", "081234567890", "budi@email.com", "gold", 5000},
430|		{"MEM000002", "Siti Rahayu", "082345678901", "siti@email.com", "silver", 3000},
431|		{"MEM000003", "Andi Pratama", "083456789012", "andi@email.com", "basic", 1500},
432|		{"MEM000004", "Dewi Lestari", "084567890123", "dewi@email.com", "gold", 7500},
433|		{"MEM000005", "Rizky Firmansyah", "085678901234", "rizky@email.com", "silver", 2200},
434|	}
435|	for _, m := range members {
436|		db.Exec("INSERT OR IGNORE INTO members (member_id,name,phone,email,points,tier) VALUES (?,?,?,?,?,?)",
437|			m.mid, m.name, m.phone, m.email, m.points, m.tier)
438|	}
439|
440|}
441|
442|func now() string {
443|	return time.Now().Format("2006-01-02 15:04:05")
444|}
445|
```

---

## server.go

```
1|package main
2|
3|import (
4|	"embed"
5|	"fmt"
6|	"log"
7|	"net/http"
8|	"os"
9|	"os/exec"
10|	"path/filepath"
11|	"runtime"
12|	"strings"
13|	"time"
14|
15|	"github.com/gorilla/websocket"
16|)
17|
18|//go:embed frontend/*
19|var frontendFS embed.FS
20|
21|
22|var upgrader = websocket.Upgrader{
23|	CheckOrigin: func(r *http.Request) bool {
24|		origin := r.Header.Get("Origin")
25|		if origin == "" || strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1") {
26|			return true
27|		}
28|		return false
29|	},
30|}
31|
32|func handleWebSocket(w http.ResponseWriter, r *http.Request) {
33|	conn, err := upgrader.Upgrade(w, r, nil)
34|	if err != nil {
35|		return
36|	}
37|	wsMu.Lock()
38|	wsClients[conn] = true
39|	wsMu.Unlock()
40|
41|	defer func() {
42|		wsMu.Lock()
43|		delete(wsClients, conn)
44|		wsMu.Unlock()
45|		conn.Close()
46|	}()
47|
48|	for {
49|		_, _, err := conn.ReadMessage()
50|		if err != nil {
51|			break
52|		}
53|	}
54|}
55|
56|func main() {
57|	initDB()
58|	defer db.Close()
59|	go cleanupSessions()
60|
61|	mux := http.NewServeMux()
62|
63|	// Auth helper: wrap admin-only endpoints
64|	adminOnly := func(next http.HandlerFunc) http.HandlerFunc {
65|		return func(w http.ResponseWriter, r *http.Request) {
66|			if !requireAuth(r, "admin") {
67|				jsonResponse(w, map[string]string{"error": "Unauthorized"}, 401)
68|				return
69|			}
70|			next(w, r)
71|		}
72|	}
73|
74|	// API routes
75|	mux.HandleFunc("/api/login", handleLogin)
76|	mux.HandleFunc("/api/csrf-token", handleGetCSRFToken)
77|	mux.HandleFunc("/api/logout", handleLogout)
78|	mux.HandleFunc("/api/change-password", handleChangePassword)
79|	mux.HandleFunc("/api/users", adminOnly(handleGetUsers))
80|	mux.HandleFunc("/api/products", func(w http.ResponseWriter, r *http.Request) {
81|		if r.Method == "GET" {
82|			handleGetProducts(w, r)
83|		} else if r.Method == "POST" {
84|			adminOnly(handleAddProduct)(w, r)
85|		}
86|	})
87|	mux.HandleFunc("/api/products/", func(w http.ResponseWriter, r *http.Request) {
88|		if r.Method == "PUT" {
89|			adminOnly(handleUpdateProduct)(w, r)
90|		} else if r.Method == "DELETE" {
91|			adminOnly(handleDeleteProduct)(w, r)
92|		}
93|	})
94|	mux.HandleFunc("/api/categories", handleGetCategories)
95|	mux.HandleFunc("/api/shifts/open", handleOpenShift)
96|	mux.HandleFunc("/api/shifts/active", handleGetActiveShifts)
97|	mux.HandleFunc("/api/shifts", func(w http.ResponseWriter, r *http.Request) {
98|		if r.Method == "POST" {
99|			handleOpenShift(w, r)
100|		} else {
101|			handleGetShifts(w, r)
102|		}
103|	})
104|	mux.HandleFunc("/api/shifts/", func(w http.ResponseWriter, r *http.Request) {
105|		if r.Method == "POST" {
106|			if strings.HasSuffix(r.URL.Path, "/close-self") {
107|				handleCloseShiftSelf(w, r)
108|			} else {
109|				adminOnly(handleCloseShift)(w, r)
110|			}
111|		}
112|	})
113|	mux.HandleFunc("/api/cash/drop", adminOnly(handleCashDrop))
114|	mux.HandleFunc("/api/cash/in", adminOnly(handleCashIn))
115|	mux.HandleFunc("/api/cash/log/", handleGetCashLog)
116|	mux.HandleFunc("/api/members", func(w http.ResponseWriter, r *http.Request) {
117|		if r.Method == "GET" {
118|			handleGetMembers(w, r)
119|		} else if r.Method == "POST" {
120|			handleAddMember(w, r)
121|		}
122|	})
123|	mux.HandleFunc("/api/members/", handleGetMember)
124|	mux.HandleFunc("/api/checkout", handleCheckout)
125|	mux.HandleFunc("/api/hold", func(w http.ResponseWriter, r *http.Request) {
126|		if r.Method == "GET" {
127|			handleGetHolds(w, r)
128|		} else if r.Method == "POST" {
129|			handleHold(w, r)
130|		}
131|	})
132|	mux.HandleFunc("/api/holds/", func(w http.ResponseWriter, r *http.Request) {
133|		if r.Method == "DELETE" {
134|			handleDeleteHold(w, r)
135|		}
136|	})
137|	mux.HandleFunc("/api/transactions", func(w http.ResponseWriter, r *http.Request) {
138|		if r.Method == "GET" {
139|			handleGetTransactions(w, r)
140|		}
141|	})
142|	mux.HandleFunc("/api/transactions/", func(w http.ResponseWriter, r *http.Request) {
143|		if r.Method == "PUT" {
144|			handleVoidTransaction(w, r)
145|		}
146|	})
147|	mux.HandleFunc("/api/stats", handleGetStats)
148|	mux.HandleFunc("/api/sales-trend", handleSalesTrend)
149|	mux.HandleFunc("/api/payment-breakdown", handlePaymentBreakdown)
150|	mux.HandleFunc("/api/daily-report", handleDailyReport)
151|	mux.HandleFunc("/api/stock-report", handleStockReport)
152|	mux.HandleFunc("/api/e-voucher", func(w http.ResponseWriter, r *http.Request) {
153|		if r.Method == "GET" {
154|			handleGetEVouchers(w, r)
155|		} else if r.Method == "POST" {
156|			handleEVoucher(w, r)
157|		}
158|	})
159|	mux.HandleFunc("/api/quick-access", handleQuickAccess)
160|	mux.HandleFunc("/api/receipt/", handleReceipt)
161|	mux.HandleFunc("/api/alerts/low-stock", handleLowStock)
162|	mux.HandleFunc("/api/backup", adminOnly(handleBackup))
163|	mux.HandleFunc("/api/restore", adminOnly(handleRestore))
164|	mux.HandleFunc("/api/ai/webhook", handleAIWebhook)
165|	mux.HandleFunc("/api/display-token", handleGenerateDisplayToken)
166|	mux.HandleFunc("/api/ai/restock-candidates", adminOnly(handleRestockCandidates))
167|	mux.HandleFunc("/api/ai/report", adminOnly(handleAIReport))
168|	mux.HandleFunc("/api/ai/settings", func(w http.ResponseWriter, r *http.Request) {
169|		if r.Method == "GET" {
170|			handleGetAISettings(w, r)
171|		} else if r.Method == "PUT" {
172|			adminOnly(handleUpdateAISettings)(w, r)
173|		}
174|	})
175|	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
176|		if r.Method == "GET" {
177|			handleGetSettings(w, r)
178|		} else if r.Method == "PUT" {
179|			adminOnly(handleUpdateSettings)(w, r)
180|		}
181|	})
182|	mux.HandleFunc("/api/ws-broadcast", handleWSBroadcast)
183|	mux.HandleFunc("/ws", handleWebSocket)
184|	mux.HandleFunc("/health", handleHealth)
185|
186|	// Frontend routes (embedded)
187|	frontendHandler := func(name string) http.HandlerFunc {
188|		return func(w http.ResponseWriter, r *http.Request) {
189|			data, err := frontendFS.ReadFile("frontend/" + name)
190|			if err != nil {
191|				http.Error(w, "Not found", 404)
192|				return
193|			}
194|			w.Header().Set("Content-Type", "text/html; charset=utf-8")
195|			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
196|			w.Header().Set("Pragma", "no-cache")
197|			w.Header().Set("Expires", "0")
198|			w.Write(data)
199|		}
200|	}
201|
202|	mux.HandleFunc("/kasir", frontendHandler("kasir.html"))
203|	mux.HandleFunc("/admin", frontendHandler("admin.html"))
204|	mux.HandleFunc("/customer", frontendHandler("customer.html"))
205|	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
206|		data, _ := frontendFS.ReadFile("frontend/sw.js")
207|		w.Header().Set("Content-Type", "application/javascript")
208|		w.Write(data)
209|	})
210|	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
211|		data, _ := frontendFS.ReadFile("frontend/manifest.json")
212|		w.Header().Set("Content-Type", "application/json")
213|		w.Write(data)
214|	})
215|	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
216|		if r.URL.Path == "/" {
217|			frontendHandler("index.html")(w, r)
218|		} else {
219|			http.NotFound(w, r)
220|		}
221|	})
222|	mux.HandleFunc("/receipt", frontendHandler("receipt.html"))
223|	mux.HandleFunc("/admin-login", frontendHandler("admin-login.html"))
224|	mux.HandleFunc("/admin-dashboard", frontendHandler("admin.html")) // redirect to admin
225|
226|	port := "8070"
227|	if p := os.Getenv("PORT"); p != "" {
228|		port = p
229|	}
230|
231|	fmt.Printf("[POS] Server starting on http://localhost:%s/\n", port)
232|	fmt.Printf("[POS] Data dir: %s\n", getDataDir())
233|	fmt.Printf("[POS] Version: 2.2 (Go)\n")
234|
235|	// Auto-open browser
236|	go func() {
237|		time.Sleep(2 * time.Second)
238|		url := "http://localhost:" + port + "/"
239|		cacheBust := fmt.Sprintf("%d", time.Now().UnixMilli())
240|		freshURL := url + "?v=" + cacheBust
241|		fmt.Printf("[POS] Opening browser: %s\n", freshURL)
242|		if runtime.GOOS == "windows" {
243|			// Use separate Chrome profile for POS (always kiosk-printing)
244|		myPath, _ := os.Executable()
245|		posDir := filepath.Dir(myPath)
246|		chromeProfile := filepath.Join(posDir, "chrome-pos-profile")
247|		exec.Command("cmd", "/c", "start", "chrome",
248|			"--user-data-dir="+chromeProfile,
249|			"--kiosk-printing",
250|			"--disable-features=TranslateUI",
251|			"--no-first-run",
252|			"--disable-session-crashed-bubble",
253|			freshURL).Start()
254|		} else if runtime.GOOS == "darwin" {
255|			exec.Command("open", freshURL).Start()
256|		} else {
257|			exec.Command("xdg-open", freshURL).Start()
258|		}
259|	}()
260|
261|// Auto-start Cloudflare Tunnel ONLY if ENABLE_ADMIN_TUNNEL=true
262|	if os.Getenv("ENABLE_ADMIN_TUNNEL") != "true" {
263|		fmt.Println("[POS] Admin tunnel DISABLED (set ENABLE_ADMIN_TUNNEL=true to enable)")
264|	} else {
265|	go func() {
266|		time.Sleep(3 * time.Second)
267|		exePath, _ := os.Executable()
268|		exeDir := filepath.Dir(exePath)
269|		cloudflared := filepath.Join(exeDir, "cloudflared.exe")
270|		if _, err := os.Stat(cloudflared); os.IsNotExist(err) {
271|			fmt.Printf("[POS] cloudflared.exe not found in %s\n", exeDir)
272|			return
273|		}
274|		fmt.Printf("[POS] Found cloudflared at: %s\n", cloudflared)
275|		fmt.Printf("[POS] Starting Cloudflare Tunnel...\n")
276|		fmt.Printf("[POS] Buka terminal cloudflared untuk lihat URL\n")
277|		fmt.Printf("[POS] Atau jalankan manual: ENABLE_ADMIN_TUNNEL=true cloudflared tunnel --url http://localhost:%s\n", port)
278|		// Open cloudflared in separate console window
279|		if runtime.GOOS == "windows" {
280|			cmd := exec.Command("cmd", "/c", "start", "cmd", "/k", cloudflared, "tunnel", "--url", "http://localhost:"+port)
281|			cmd.Start()
282|		} else {
283|			cmd := exec.Command(cloudflared, "tunnel", "--url", "http://localhost:"+port)
284|			cmd.Start()
285|		}
286|	}()
287|	}
288|
289|log.Fatal(http.ListenAndServe(":"+port, mux))
290|}
291|
```

---

## .gitignore

```
1|# Secrets and local deployment config
2|config.json
3|.env
4|.env.*
5|*.pem
6|*.key
7|*.crt
8|
9|# Local databases and WAL files
10|*.db
11|*.db-shm
12|*.db-wal
13|pos_replica.db
14|
15|# Binaries and local runtime data
16|pos-server
17|pos-server.exe
18|cloudflared.exe
19|chrome-pos-profile/
20|POS_Simulator.exe
21|
22|# Editor and OS files
23|.vscode/
24|.idea/
25|.DS_Store
26|Thumbs.db
27|
28|# Test artifacts and coverage
29|coverage.out
30|*.test
31|
```

---

## config.example.json

```
1|{
2|  "turso_url": "",
3|  "turso_token": ""
4|}
5|
```

---

