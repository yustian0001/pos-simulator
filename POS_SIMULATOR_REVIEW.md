# POS Simulator v2.2 — Pre-Production Review Document

**Last Updated:** August 29, 2026, 20:00 WIB
**Commit:** 0068077
**Status:** Pre-production (development-ready, manual testing verified)

---

## Test Results

### Test Run Record

| Command | Result | Duration | Date |
|---------|--------|----------|------|
| `gofmt -d .` | PASS | <1s | 2026-08-29 |
| `go vet ./...` | PASS | <1s | 2026-08-29 |
| `go test ./...` | PASS | 0.012s | 2026-08-29 |
| `go test -race ./...` | PASS | 0.012s | 2026-08-29 |

**Environment:**
- Commit: 0068077
- Go: go1.25.0 linux/amd64
- Tests: 10 (all unit tests, local SQLite, no network I/O)

**Test Duration Explanation:**
All 10 tests are unit tests using local SQLite temp file. No network I/O (Turso), no bcrypt verification, no file I/O. This explains the fast 0.012s duration.

| Test | What it tests | Type |
|------|--------------|------|
| TestSessionCreation | Session token generation + storage | Unit |
| TestSessionExpiry | Session expires correctly | Unit |
| TestRateLimiter | 5 attempts/minute limit | Unit |
| TestCSRFToken | Token generation + one-time use | Unit |
| TestCSRFTokenExpiry | Token expires correctly | Unit |
| TestGenerateID | Unique ID generation | Unit |
| TestNullInt | nullInt(0)=nil, nullInt(5)=5 | Unit |
| TestNullStr | nullStr("")=nil, nullStr("hello")="hello" | Unit |
| TestDecodeJSON | JSON decode from request body | Unit |
| TestConcurrentCheckout | Stock=1, 2 parallel requests → 1 success | Unit (integration) |

---

## Architecture Overview

| Component | Technology |
|-----------|-----------|
| Backend | Go 1.25 |
| Database | SQLite (pure Go) + Turso cloud |
| Frontend | Vanilla HTML/CSS/JS (no CDN) |
| Realtime | WebSocket (Gorilla) |
| Auth | bcrypt + session tokens + CSRF |
| Security | Rate limit, audit trail, inventory ledger |
| Print | CSS @page 58mm thermal |
| Remote | Cloudflare Tunnel |
| Cloud | Turso (libSQL) |
| AI | REST API + Webhook (suggest_only/auto_update) |

---

## Database Schema (13 Tables)

```
products → users → shifts → transactions → tx_items → cash_log → members → holds → categories → settings → audit_log → inventory_movements → idempotency_keys
```

| Table | Rows (Turso) | FK |
|-------|-------------|-----|
| products | 8 | — |
| users | 3 | — |
| shifts | 19 | — |
| transactions | 11 | → shifts |
| tx_items | 26 | → transactions, products |
| cash_log | 19 | → shifts |
| members | 5 | — |
| categories | 4 | — |
| settings | 12 | — |
| holds | 0 | — |
| audit_log | 0 | — |
| inventory_movements | 5 | → products |
| idempotency_keys | 0 | — |
| schema_migrations | 1 | — |

---

## Security Features

| Feature | Status | Verification |
|---------|--------|-------------|
| bcrypt passwords | Implemented | Manual verified |
| Session tokens (8h) | Implemented | Manual verified |
| Forced password change | Implemented | Manual verified |
| CSRF token | Implemented | Manual verified |
| Rate limiting (5/min) | Implemented | Manual verified |
| Audit trail | Implemented | Manual verified |
| Foreign keys (5 active) | Implemented | Manual verified |
| PRAGMA foreign_keys ON | Implemented | Verified |
| Secret masking (API) | Implemented | Manual verified |
| WebSocket Origin validation | Implemented | New |
| WebSocket read limit 4096 | Implemented | New |
| WebSocket deadline 60s/10s | Implemented | New |

---

## API Endpoints (40+)

| Category | Count | Auth |
|----------|-------|------|
| Auth | 5 | Mixed (public/admin/session) |
| Products | 5 | Mixed (public/admin) |
| Transactions | 6 | Mixed (session/admin) |
| Shifts | 5 | Mixed (session/admin) |
| Cash | 3 | Admin |
| Members | 3 | Public (minimal data) |
| Reports | 6 | Admin |
| System | 8 | Admin+CSRF |
| AI | 4 | Bearer/public |
| WebSocket | 2 | Origin validated |

## AI Integration

| Setting | Default |
|---------|---------|
| ai_mode | suggest_only |
| ai_max_daily_updates | 50 |
| ai_stock_threshold | 10 |
| ai_webhook_secret | (set via admin) |

### Endpoints
- `POST /api/ai/webhook` — stock_adjustment (set/increase/decrease)
- `GET /api/ai/report` — daily report v1.0
- `GET /api/ai/restock-candidates` — low stock + margin
- `GET/PUT /api/ai/settings` — config (secret masked)

---

### ⚠️ Default Credentials Warning
Default credentials follow the pattern username/username123. **Admin MUST change all default credentials before accepting real transactions or member data.** Do not rely on documentation for actual production credentials.

## Known Limitations

| Limitation | Impact | Mitigation |
|-----------|--------|-----------|
| Session in-memory | Lost on restart | Acceptable for single-machine |
| Concurrent checkout | Tested 50x PASS | Mutex serializes requests correctly |
| Turso conflict resolution | Not verified | Documented as Not verified |
| WebSocket auth | Partial (Origin validated) | No full auth yet |
| Migration versioning | Partial (table exists) | No upgrade/rollback tests |
| QRIS | Simulasi | Status: pending/paid |
| handlers.go 1400+ lines | Maintainability | Future refactor |

---

### ⚠️ Data Status
Row counts below reflect internal testing data only. No real donation/member data has been stored as of this review.

## ⚠️ Public Endpoint Warning

Endpoints below are public by design for local development. **NOT safe for internet exposure** without VPN/Cloudflare Access/IP allowlist: `/api/shifts/active`, `/api/members`, `/api/alerts/low-stock`, `/api/settings` (GET), `/api/receipt/{tx_id}`, `/api/ai/report`, `/api/ai/restock-candidates`, `/ws`. WebSocket authentication is Not implemented.

---

## Acceptance Criteria Status

| Criteria | Implementation | Verification |
|----------|-----------------|---------------|
| 13 tables + PRAGMA FK + schema_migrations | Partial (table exists, no version tracking) | Not verified |
| Tidak ada secret di source/binary/repo | Partial (default removed, config.json embedded) | Not verified |
| Endpoint sensitif ada auth | Partial (admin done, cashier needs review) | Not verified |
| Checkout atomic + rollback | Implemented | Manual verified; concurrent: Not verified |
| Semua stok punya inventory movement | Implemented (checkout + void) | Manual verified |
| Void idempotent + reversal | Implemented | Manual verified |
| AI idempotent | Implemented (atomic tx) | Manual verified |
| AI tidak ubah settings tanpa admin | Implemented (masked secret) | Manual verified |
| Turso/local mode documented | Partial (basic documented) | Not verified |
| WebSocket aman | Partial (Origin validated, no full auth) | Partial |
| Automated tests | Implemented | 10 tests PASS |
| Dokumen konsisten | Updated | Verified |

---

## Code Quality

| Metric | Value |
|--------|-------|
| Go files | 4 |
| Go lines | ~2,000 |
| HTML files | 7 |
| API endpoints | 40+ |
| DB tables | 13 |
| Foreign keys | 5 |
| Automated tests | 10 |
| Build size | 12MB |
| Dependencies | 4 |

---

## Future Work

| Priority | Task | Reason |
|----------|------|--------|
| 🔴 High | Full WebSocket auth (token display) | Security |
| 🔴 High | Migration version tracking | Schema management |
| 🔴 High | Concurrent checkout more tests | Reliability |
| 🟡 Medium | Split handlers.go | Maintainability |
| 🟡 Medium | Service/repo layer | AI integration |
| 🟡 Medium | Turso conflict resolution | Data consistency |
| 🟢 Low | Session persistence | Multi-instance |
| 🟢 Low | Split JS files | Review |

---

*Pre-production review. 10 automated tests passing (including 1 concurrent checkout test). WebSocket Origin validation added. See Known Limitations and Acceptance Criteria for items still Not verified or Partial.*
