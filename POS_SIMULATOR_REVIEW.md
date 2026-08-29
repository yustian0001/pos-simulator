# POS Simulator v2.2 — Pre-Production Review Document

**Last Updated:** August 29, 2026, 22:50 WIB
**Commit:** 2f503ed
**Status:** Pre-production (development-ready)

---

## Test Results

| Command | Result | Duration | Date |
|---------|--------|----------|------|
| `gofmt -d .` | PASS | <1s | 2026-08-29 |
| `go vet ./...` | PASS | <1s | 2026-08-29 |
| `go test ./...` | PASS | 0.011s | 2026-08-29 |
| `go test -race ./...` | PASS | 0.011s | 2026-08-29 |
| `TestConcurrentCheckout -count=50` | PASS (50/50) | 0.105s | 2026-08-29 |

**Environment:** Commit 2f503ed | Go go1.25.0 | linux/amd64 | 10 tests (all unit, local SQLite)

**Test Duration Explanation:** All 10 tests are unit tests using local SQLite temp file. No network I/O, no bcrypt, no file I/O. This explains the fast 0.011s duration.

| Test | What it tests | Type |
|------|--------------|------|
| TestSessionCreation | Token generation + storage | Unit |
| TestSessionExpiry | Token expires correctly | Unit |
| TestRateLimiter | 5 attempts/minute limit | Unit |
| TestCSRFToken | Token generation + one-time use | Unit |
| TestCSRFTokenExpiry | Token expires correctly | Unit |
| TestGenerateID | Unique ID generation | Unit |
| TestNullInt | nullInt(0)=nil, nullInt(5)=5 | Unit |
| TestNullStr | nullStr("")=nil | Unit |
| TestDecodeJSON | JSON decode from request body | Unit |
| TestConcurrentCheckout | Stock=1, 2 parallel → 1 success | Unit (integration) |

---

## Architecture

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

## Database (13 Tables + Migration)

| Table | FK |
|-------|-----|
| products | — |
| users | — |
| shifts | — |
| transactions | → shifts |
| tx_items | → transactions, products |
| cash_log | → shifts |
| members | — |
| categories | — |
| settings | — |
| holds | — |
| audit_log | — |
| inventory_movements | → products |
| idempotency_keys | — |
| schema_migrations | — |

### Migration System
- `MIGRATION_VERSION` constant in handlers.go
- `checkMigration()` runs at startup
- Compares current DB version vs code version
- Auto-upgrades: ALTER TABLE + INSERT schema_migrations
- Logs migration status to console

---

## Security

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
| WebSocket Origin validation | Implemented | Verified |
| WebSocket read limit 4096 | Implemented | Verified |
| WebSocket deadline 60s/10s | Implemented | Verified |
| WebSocket display token | Implemented | Not verified |
| Shift ownership check | Implemented | Not verified |
| Hold auth + audit | Implemented | Not verified |
| Migration versioning | Implemented | Not verified |

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

---

## ⚠️ Default Credentials Warning

Default credentials follow the pattern username/username123. **Admin MUST change all default credentials before accepting real transactions or member data.** Do not rely on documentation for actual production credentials.

---

## ⚠️ Data Status

Row counts below reflect internal testing data only. No real donation/member data has been stored as of this review.

---

## ⚠️ Public Endpoint Warning

Endpoints below are public by design for local development. **NOT safe for internet exposure** without VPN/Cloudflare Access/IP allowlist: `/api/shifts/active`, `/api/members`, `/api/alerts/low-stock`, `/api/settings` (GET), `/api/receipt/{tx_id}`, `/api/ai/report`, `/api/ai/restock-candidates`, `/ws`.

---

---

## Cashier Endpoint Audit

| Endpoint | Expected Actor | Ownership Check | Status |
|----------|---------------|-----------------|--------|
| POST /api/checkout | cashier (own shift) | Shift ownership verified | Not verified |
| POST /api/shifts/{id}/close-self | cashier (own shift) | Yes (new) | Not verified |
| POST /api/hold | cashier | Session required | Not verified |
| DELETE /api/holds/{id} | cashier (own hold) | Yes (new) | Not verified |
| GET /api/holds | cashier | Session required | Not verified |

**Ownership checks implemented:** shift ownership (cashier=shift.cashier), hold auth (session required).
**Ownership checks pending verification:** automated tests needed for each row above.

---

## Known Limitations

| Limitation | Impact | Mitigation |
|-----------|--------|-----------|
| Session in-memory | Lost on restart | Acceptable for single-machine |
| Concurrent checkout | Tested 50x PASS | Mutex serializes requests correctly |
| Turso conflict resolution | Partial (3 scenarios documented) | Not verified |
| WebSocket auth | Partial (Origin + display token; channel separation & audit log pending) |
| Migration versioning | Implemented | Not verified |
| QRIS | Simulasi | Status: pending/paid |
| handlers.go 1400+ lines | Maintainability | Future refactor |

---

## Acceptance Criteria Status

| Criteria | Implementation | Verification |
|----------|-----------------|---------------|
| 13 tables + PRAGMA FK + schema_migrations | Implemented | Not verified |
| Tidak ada secret di source/binary/repo | Partial (config.json embedded) | Not verified |
| Endpoint sensitif ada auth | Partial (admin done, cashier: shift ownership + hold auth added) | Not verified |
| Checkout atomic + rollback | Implemented | Manual verified; concurrent: Tested 50x PASS |
| Semua stok punya inventory movement | Implemented (checkout + void) | Manual verified |
| Void idempotent + reversal | Implemented | Manual verified |
| AI idempotent | Implemented (atomic tx) | Manual verified |
| AI tidak ubah settings tanpa admin | Implemented (masked secret) | Manual verified |
| Turso/local mode documented | Partial (basic documented) | Not verified |
| WebSocket aman | Implemented (Origin validation + display token + read limit + deadline) | Not verified |
| Automated tests | Implemented | 10 tests PASS |
| Migration versioning | Implemented | Not verified |
| Dokumen konsistent | Updated | Verified |

---

## Future Work

| Priority | Task | Reason |
|----------|------|--------|
| 🔴 High | Turso conflict resolution | Data consistency |
| 🟡 Medium | WebSocket channel separation + connection audit log | Security |
| 🟡 Medium | Endpoint auth cashier review | Security |
| 🟡 Medium | Split handlers.go | Maintainability |
| 🟡 Medium | Service/repo layer | AI integration |
| 🟢 Low | Session persistence | Multi-instance |
| 🟢 Low | Split JS files | Review |


---

## Turso Conflict Scenarios (Draft)

### Skenario A — Offline write → reconnect sync (paling mungkin)
```
Instance offline → tulis transaksi ke SQLite lokal →
koneksi Turso pulih → bagaimana data lokal disinkronkan ke Turso?
```
**Strategi:** Last-write-wins berdasarkan `created_at` lokal. Server syncs local transactions to Turso on reconnect. Conflicts resolved by timestamp (newer wins).

### Skenario B — Concurrent writes (kurang mungkin untuk single-register)
```
Dua instance menulis ke Turso bersamaan →
siapa yang menang jika keduanya mengubah stok produk yang sama?
```
**Strategi:** SQLite-level conflict resolution (last-write-wins on row level). For single-register POS, this scenario is unlikely but documented for completeness.

### Skenario C — Turso down di tengah transaksi
```
Turso down di tengah checkout yang sedang menulis →
apakah otomatis fallback ke SQLite lokal?
```
**Strategi:** Fallback to local SQLite immediately. Transaction completes locally. On reconnect, sync to Turso. No data loss if local write succeeds.

**Status:** Scenarios documented. Skenario A implementation pending.

---

*Pre-production review. 10 automated tests passing, including concurrent checkout tested 50x with consistent PASS results. Migration versioning implemented. See Known Limitations and Acceptance Criteria for items still Not verified or Partial.*
