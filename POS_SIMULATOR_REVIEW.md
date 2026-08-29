# POS Simulator v2.2 — Comprehensive Review Document

**Last Updated:** August 29, 2026
**Version:** 2.2 (Go)
**Repository:** https://github.com/yustian0001/pos-simulator

---

## 1. Architecture Overview

| Component | Technology | Purpose |
|-----------|-----------|---------|
| **Backend** | Go 1.25 | Single binary .exe, no runtime dependencies |
| **Database** | SQLite (pure Go) + Turso cloud | Offline-first + cloud backup |
| **Frontend** | Vanilla HTML/CSS/JS | Single-file, no CDN, no build step |
| **Realtime** | WebSocket (Gorilla) | Customer display live updates |
| **Auth** | bcrypt + session tokens | 8h expiry, auto cleanup |
| **Print** | CSS @page 58mm | Auto-print via Chrome --kiosk-printing |
| **Remote** | Cloudflare Tunnel | Auto-detect cloudflared.exe |
| **Cloud** | Turso (libSQL) | Embedded config, auto-fallback |

### Build Command
```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"
```

**Output:** Single 12MB .exe (PE32+, 8 sections, stripped debug info)

---

## 2. Project Structure

```
pos-go/
├── main.go              (385 lines) — Models, DB init, seed data, session store, cleanup
├── server.go            (226 lines) — HTTP routes, WebSocket, browser open, Cloudflare tunnel
├── handlers.go          (1321 lines) — All API logic (shared utilities + handlers)
├── config.json          (embedded) — Turso credentials via //go:embed
├── go.mod / go.sum      — Dependencies
├── frontend/
│   ├── index.html       — Landing page (3 role cards: Admin/Kasir/Customer)
│   ├── kasir.html       — Cashier: login → shift → cart → bayar → print
│   ├── admin.html       — Admin: 9 tabs (full CRUD)
│   ├── admin-login.html — Admin login
│   ├── customer.html    — Customer display (WebSocket real-time)
│   └── receipt.html     — Print receipt 58mm thermal
```

### Dependencies (go.mod)
```
github.com/gorilla/websocket v1.5.3
golang.org/x/crypto v0.55.0
modernc.org/sqlite v1.57.0
github.com/tursodatabase/libsql-client-go v0.0.0-20260528
```

---

## 3. Database Schema

### Tables (11 total)

```sql
-- Products with per-product tax rate
CREATE TABLE products (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sku TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
    price INTEGER DEFAULT 0, cost INTEGER DEFAULT 0,
    category TEXT DEFAULT 'Umum', stock INTEGER DEFAULT 0,
    unit TEXT DEFAULT 'pcs', barcode TEXT DEFAULT '',
    promo_price INTEGER DEFAULT 0, promo_active INTEGER DEFAULT 0,
    tax_rate REAL DEFAULT -1,  -- -1 = global PPN, 0 = no tax, >0 = custom
    active INTEGER DEFAULT 1, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Users (admin + kasir)
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL, password TEXT NOT NULL,  -- bcrypt hashed
    display_name TEXT DEFAULT '', role TEXT DEFAULT 'kasir',
    active INTEGER DEFAULT 1
);

-- Transactions with foreign keys
CREATE TABLE transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tx_id TEXT UNIQUE NOT NULL, shift_id INTEGER,
    total INTEGER DEFAULT 0, discount INTEGER DEFAULT 0,
    tax INTEGER DEFAULT 0, grand_total INTEGER DEFAULT 0,
    payment TEXT DEFAULT 'CASH', amount_paid INTEGER DEFAULT 0,
    change_amount INTEGER DEFAULT 0, customer_name TEXT DEFAULT '',
    member_id INTEGER DEFAULT NULL, cashier TEXT DEFAULT 'kasir',
    notes TEXT DEFAULT '', status TEXT DEFAULT 'completed',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (shift_id) REFERENCES shifts(id)
);

-- Transaction items with FK to products
CREATE TABLE tx_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tx_id TEXT NOT NULL, product_id INTEGER,
    name TEXT NOT NULL, qty INTEGER DEFAULT 1,
    price INTEGER DEFAULT 0, discount INTEGER DEFAULT 0,
    subtotal INTEGER DEFAULT 0, notes TEXT DEFAULT '',
    FOREIGN KEY (tx_id) REFERENCES transactions(tx_id),
    FOREIGN KEY (product_id) REFERENCES products(id)
);

-- Shifts
CREATE TABLE shifts (
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

-- Cash log with FK
CREATE TABLE cash_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    shift_id INTEGER, type TEXT NOT NULL,
    amount INTEGER DEFAULT 0, description TEXT DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (shift_id) REFERENCES shifts(id)
);

-- Members
CREATE TABLE members (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
    phone TEXT DEFAULT '', email TEXT DEFAULT '',
    points INTEGER DEFAULT 0, tier TEXT DEFAULT 'basic',
    active INTEGER DEFAULT 1, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Hold carts (暂时保存)
CREATE TABLE holds (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hold_id TEXT UNIQUE NOT NULL, items_json TEXT NOT NULL,
    customer_name TEXT DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Categories
CREATE TABLE categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL, icon TEXT DEFAULT '📦'
);

-- Settings (key-value)
CREATE TABLE settings (
    key TEXT PRIMARY KEY, value TEXT NOT NULL
);

-- Audit trail (NEW)
CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL, entity TEXT NOT NULL,
    entity_id TEXT DEFAULT '', user TEXT DEFAULT '',
    details TEXT DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### Foreign Key Relationships
```
tx_items.tx_id → transactions.tx_id
tx_items.product_id → products.id
transactions.shift_id → shifts.id
cash_log.shift_id → shifts.id
```

---

## 4. API Endpoints

### Authentication
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/login` | POST | ❌ | Login (returns token + csrf_token) |
| `/api/logout` | POST | ✅ | Logout + session delete |
| `/api/csrf-token` | GET | ❌ | Get CSRF token |

### Products
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/products` | GET | ❌ | List products (optional ?search, ?category, ?admin=1) |
| `/api/products` | POST | ✅ admin | Add product |
| `/api/products/{id}` | PUT | ✅ admin | Update product |
| `/api/products/{id}` | DELETE | ✅ admin | Soft delete product |
| `/api/categories` | GET | ❌ | List categories |

### Transactions
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/checkout` | POST | ❌ | Create transaction (BEGIN/COMMIT TX) |
| `/api/transactions` | GET | ✅ admin | List transactions |
| `/api/transactions/{id}/void` | PUT | ✅ admin + CSRF | Void transaction |
| `/api/hold` | POST | ❌ | Hold cart |
| `/api/holds` | GET | ❌ | List held carts |
| `/api/holds/{id}` | DELETE | ❌ | Delete held cart |

### Shifts
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/shifts/open` | POST | ❌ | Open new shift |
| `/api/shifts/active` | GET | ❌ | Get active shifts |
| `/api/shifts` | GET | ❌ | List all shifts |
| `/api/shifts/{id}/close` | POST | ✅ admin | Close shift (admin) |
| `/api/shifts/{id}/close-self` | POST | ❌ | Close shift (kasir, auto-calculate) |

### Cash
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/cash/drop` | POST | ✅ admin | Cash drop |
| `/api/cash/in` | POST | ✅ admin | Cash in |
| `/api/cash/log/{shift_id}` | GET | ❌ | Cash log per shift |

### Members
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/members` | GET | ❌ | Search members (by name/phone/ID) |
| `/api/members` | POST | ❌ | Add member |
| `/api/members/{id}` | GET | ❌ | Get member detail |

### Reports
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/stats` | GET | ❌ | Dashboard stats |
| `/api/daily-report` | GET | ❌ | Daily sales report |
| `/api/stock-report` | GET | ❌ | Stock report with values |
| `/api/sales-trend` | GET | ❌ | 7-day sales trend |
| `/api/payment-breakdown` | GET | ❌ | Payment method breakdown |
| `/api/alerts/low-stock` | GET | ❌ | Products with stock < 10 |

### System
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/backup` | GET | ✅ admin | Download pos.db |
| `/api/restore` | POST | ✅ admin + CSRF | Upload pos.db |
| `/api/settings` | GET | ❌ | Get settings |
| `/api/settings` | PUT | ✅ admin | Update settings |
| `/api/receipt/{tx_id}` | GET | ❌ | Receipt data |
| `/api/quick-access` | GET | ❌ | Quick access products |
| `/api/e-vouchers` | GET | ❌ | List e-vouchers |
| `/api/e-voucher` | POST | ❌ | Use e-voucher |
| `/health` | GET | ❌ | Health check |

### AI Integration (NEW)
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/ai/webhook` | POST | ✅ Bearer token | AI agent commands |
| `/api/ai/report` | GET | ❌ | Daily report for AI analysis |
| `/api/ai/settings` | GET/PUT | ❌/✅ admin | AI configuration |

### WebSocket
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/ws` | WS | ❌ | Real-time cart + transaction updates |
| `/api/ws-broadcast` | POST | ❌ | Send message to all WS clients |

---

## 5. Security Features

### Authentication & Authorization
- **Password hashing:** bcrypt (cost 10)
- **Session tokens:** 8-hour expiry, auto cleanup every 5 minutes
- **RBAC:** `adminOnly` middleware for admin endpoints
- **CSRF protection:** Token generated on login, validated on void/restore

### Rate Limiting
- Login: max 5 attempts per minute per IP+username
- Returns HTTP 429 with audit log entry

### Data Integrity
- **Foreign keys:** tx_items→transactions, transactions→shifts, cash_log→shifts
- **Stock validation:** SQLite transaction + `stock >= qty` atomic check
- **Input validation:** Qty > 0, stock check, member validation
- **Product IDs:** Crypto/rand (not auto-increment)

### Audit Trail
- Auto-log: login, checkout, void, restore_start
- Fields: action, entity, entity_id, user, details, timestamp

### AI Webhook Security
- Bearer token validation via `ai_webhook_secret` setting
- Idempotency: `request_id` checked in audit_log
- All actions logged to audit_log

---

## 6. Transaction Flow

```
1. Login → bcrypt verify → session token (8h) + csrf_token
2. Open Shift → shifts table (opening_cash, status='open')
3. Add to Cart → addToCart() → broadcastCart() via WebSocket
4. Customer Display → live update (kasir name, member name, items, total)
5. Member Lookup → search by phone/name/ID (autocomplete)
6. Bayar (CASH/QRIS):
   a. BEGIN TRANSACTION (SQLite)
   b. Validate stock (stock >= qty) per item
   c. Track failed items (product not found, insufficient stock)
   d. Deduct stock (atomic UPDATE)
   e. Calculate per-produk tax (custom rate or global PPN)
   f. Insert transactions + tx_items
   g. Insert cash_log
   h. Update shifts (total_sales, total_tx)
   i. Add member points (+1 per Rp 1.000)
   j. COMMIT
   k. Audit log
   l. Return warnings[] if any items failed
7. Print → receipt 58mm thermal (auto-print Chrome --kiosk-printing)
8. Customer Display → reset via WebSocket (tombol Print)
```

### Error Handling in Checkout
```json
{
  "id": "TX12345678",
  "total": 50000,
  "tax": 5500,
  "grand_total": 55500,
  "items": [...],
  "warnings": [
    "Ayam Geprek (stok: 0, diminta: 2)",
    "Produk ID 99 tidak ditemukan"
  ]
}
```

---

## 7. AI Integration

### Architecture
```
AI Agent (Hermes/OpenClaw)
    ↓ POST /api/ai/webhook (Bearer token)
POS Server
    → stock_update (update product stock)
    → audit_log (track all changes)
    ↓
POS Server
    → GET /api/ai/report (daily summary)
AI Agent
    → Analyze data
    → Send recommendations
```

### Webhook Commands
```json
// Update stock
POST /api/ai/webhook
Authorization: Bearer <ai_webhook_secret>
{
  "action": "stock_update",
  "request_id": "unique-id-123",
  "data": {
    "product_id": 1,
    "new_stock": 50,
    "reason": "Restock dari supplier ABC"
  }
}

// Get restock candidates
POST /api/ai/webhook
{
  "action": "restock_recommendation"
}
```

### Daily Report
```json
GET /api/ai/report?date=2026-08-29
{
  "date": "2026-08-29",
  "total_sales": 1250000,
  "total_tx": 45,
  "total_tax": 137500,
  "product_sales": [
    {"name": "Nasi Goreng", "qty": 25, "revenue": 550000},
    {"name": "Es Teh", "qty": 40, "revenue": 200000}
  ],
  "low_stock": [
    {"product_id": 3, "sku": "PRD003", "name": "Es Teh", "stock": 5}
  ],
  "member_activity": [
    {"name": "Budi Santoso", "member_id": "MEM000001", "tx_count": 8, "total_spent": 280000}
  ]
}
```

### AI Settings
```json
GET/PUT /api/ai/settings
{
  "ai_enable_auto_stock_update": "true",
  "ai_webhook_url": "https://agent.example.com/webhook",
  "ai_webhook_secret": "your-secret-here",
  "ai_stock_threshold": "10"
}
```

---

## 8. Frontend Features

### Kasir Dashboard
- **Login:** Pilih user (Andi/Budi) + password
- **Shift:** Buka shift dengan opening cash
- **Produk:** 4 kolom grid, search, category filter
- **Cart:** Qty +/-, member autocomplete, subtotal/tax/total
- **Bayar:** CASH (input uang + kembalian) / QRIS (QR code)
- **Print:** Auto-print 58mm thermal
- **Keyboard:** F1=search, F9=pay, Esc=close

### Admin Panel (9 tabs)
| Tab | Features |
|-----|----------|
| **Produk** | CRUD, PPN per produk, promo |
| **Shift** | Status, closing, discrepancy |
| **Cash** | Cash in/out log |
| **Member** | CRUD, tier, points |
| **Transaksi** | List, date filter, void |
| **Laporan** | Daily report, charts |
| **Stok** | Stock report, low stock alert |
| **Iklan** | Upload gambar carousel + running text |
| **Sistem** | Backup/restore, settings, PPN |

### Customer Display
- **Layout:** 70% iklan (kiri) + 30% transaksi (kanan)
- **Iklan:** Carousel gambar 16:9 + running text marquee
- **Live:** WebSocket real-time (kasir name, member, items)
- **Warna:** Gradasi merah `#8B1538` + emas `#D4AF37`
- **Resolusi:** 1920x1080

### Receipt (58mm Thermal)
- **Format:** Monospace, bold, 58mm width
- **Content:** Store name, address, items, subtotal, PPN, total, payment, change
- **Auto-print:** Chrome --kiosk-printing + separate profile

---

## 9. Cloud Backup (Turso)

### Configuration
```go
// Embedded config.json via //go:embed
var configFile []byte // contains turso_url + turso_token

// Auto-connect on startup
if tursoURL != "" && tursoToken != "" {
    connStr := tursoURL + "?authToken=" + tursoToken
    db, err = sql.Open("libsql", connStr)
}
// Fallback to local SQLite if Turso unavailable
if db == nil {
    db, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
}
```

### Features
- Embedded config in .exe (no external files needed)
- Auto-connect to Turso cloud
- Fallback to local SQLite
- Manual backup/restore via admin panel
- All data synced automatically

---

## 10. Deployment

### Files Required
```
POS_Simulator.exe   (12MB) — Application
cloudflared.exe     (53MB) — Cloudflare Tunnel (optional)
```

### Running
1. Double-click `POS_Simulator.exe`
2. Server starts on `localhost:8070`
3. Chrome opens with POS interface
4. If cloudflared.exe exists → auto-start tunnel → public URL displayed

### Access from Other Devices
- **Local:** `http://localhost:8070`
- **Remote:** Cloudflare Tunnel URL (auto-generated)
- **Admin from mobile:** Tunnel URL + `/admin-login`

---

## 11. Bug Fixes Applied

| Fix | Root Cause | Solution |
|-----|-----------|----------|
| **Product onclick** | Mixed quotes in inline JS → SyntaxError | data-attributes + `addFromCard()` |
| **Member select** | Same quote issue | data-attributes + `selectMemberFromEl()` |
| **Checkout warnings** | Items silently dropped | Return `warnings[]` in response |
| **Print dialog** | Chrome --kiosk-printing not inherited | Separate Chrome profile `chrome-pos` |
| **Member search** | Missing `oninput` handler | Added `oninput="searchMember()"` |
| **Member dropdown** | Missing `div#member-dropdown` in HTML | Added dropdown element |
| **Customer display** | No kasir/member info | Added via WebSocket data |
| **Stock column** | `tax_rate` column missing in old DB | ALTER TABLE migration |
| **Turso init** | Table creation only in local path | Unified init for both paths |
| **Cloudflare URL** | Parsed cloudflare terms URL | Filter for trycloudflare.com only |

---

## 12. Test Results

| Test | Result |
|------|--------|
| Login (admin/kasir) | ✅ bcrypt verify, token + CSRF |
| Product CRUD | ✅ Create, read, update, soft delete |
| Checkout (CASH) | ✅ Stock deduction, tax calc, audit log |
| Checkout (QRIS) | ✅ QR code generation |
| Member search | ✅ By phone, name, or ID |
| Shift open/close | ✅ Auto-calculate closing cash |
| Void transaction | ✅ Admin only + CSRF |
| Stock report | ✅ With low stock alerts |
| Daily report | ✅ Sales, tax, per-product |
| Backup/restore | ✅ Admin only + audit log |
| WebSocket | ✅ Cart + transaction live |
| Customer display | ✅ Iklan + live cart + kasir/member |
| Receipt print | ✅ 58mm thermal format |
| Rate limiting | ✅ 5 attempts/minute |
| CSRF validation | ✅ Token on login, checked on void |
| AI webhook | ✅ Bearer token + idempotency |
| Foreign keys | ✅ tx_items, transactions, cash_log |

---

## 13. Future Work

### Priority 1 (High)
- [ ] Split `handlers.go` into domain files (products, transactions, shifts, members)
- [ ] Add service/repository layer for AI agent integration

### Priority 2 (Medium)
- [ ] Session persistence (file-based or SQLite table)
- [ ] Multi-user session support
- [ ] CSRF token in frontend forms

### Priority 3 (Low)
- [ ] Split JS files (kasir.js, admin.js, customer.js)
- [ ] WebSocket authentication
- [ ] Database migration system
- [ ] API versioning

---

## 14. Configuration

### Settings Keys
| Key | Default | Description |
|-----|---------|-------------|
| `store_name` | Masjid Jami' Baiturrahman | Store name (receipt + display) |
| `store_address` | Jl. Tole Iskandar... | Store address |
| `store_phone` | 081234567890 | Store phone |
| `opening_cash` | 500000 | Default opening cash |
| `ppn_rate` | 11 | Global PPN % |
| `ad_title` | Promo Spesial... | Customer display title |
| `ad_desc` | ... | Customer display description |
| `ad_marquee` | ... | Running text |
| `ad_images` | [] | Carousel images (base64) |
| `qris_merchant` | POS Simulator | QRIS merchant name |
| `ai_enable_auto_stock_update` | false | AI auto stock |
| `ai_webhook_url` | — | AI webhook URL |
| `ai_webhook_secret` | — | AI webhook secret |
| `ai_stock_threshold` | 10 | Low stock threshold |

### Default Credentials
| User | Password | Role |
|------|----------|------|
| admin | admin123 | Admin |
| kasir1 | kasir123 | Kasir (Andi) |
| kasir2 | kasir123 | Kasir (Budi) |

---

## 15. Code Quality Metrics

| Metric | Value |
|--------|-------|
| Total Go files | 3 |
| Total Go lines | ~1,800 |
| Total HTML files | 6 |
| Total HTML lines | ~1,500 |
| API endpoints | 35+ |
| Database tables | 11 |
| Foreign keys | 4 |
| Audit log events | 3 (login, checkout, void) |
| Build size | 12MB (stripped) |
| Dependencies | 4 (websocket, crypto, sqlite, libsql) |

---

*Document generated for Perplexity review. Last updated: August 29, 2026.*
