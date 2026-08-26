
import httpx, json, time

BASE = "http://localhost:8070"
c = httpx.Client(timeout=5)

print("=== API TESTS (Go Server) ===")

# 1. Health
r = c.get(f"{BASE}/health")
assert r.status_code == 200
d = r.json()
assert d["service"] == "pos-server-go"
print(f"  OK: /health -> {d}")

# 2. Login
r = c.post(f"{BASE}/api/login", json={"username":"kasir1","password":"kasir123"})
assert r.status_code == 200 and r.json()["status"] == "ok"
print(f"  OK: /api/login -> {r.json()['display_name']}")

# 3. Login fail
r = c.post(f"{BASE}/api/login", json={"username":"kasir1","password":"wrong"})
assert r.status_code == 401
print("  OK: /api/login (wrong) -> 401")

# 4. Users
r = c.get(f"{BASE}/api/users")
assert r.status_code == 200 and len(r.json()) == 3
print(f"  OK: /api/users -> {len(r.json())}")

# 5. Products
r = c.get(f"{BASE}/api/products")
assert r.status_code == 200 and len(r.json()) == 8
print(f"  OK: /api/products -> {len(r.json())}")

# 6. Categories
r = c.get(f"{BASE}/api/categories")
assert r.status_code == 200 and len(r.json()) == 4
print(f"  OK: /api/categories -> {len(r.json())}")

# 7. Shift open
r = c.post(f"{BASE}/api/shifts/open", json={"cashier":"kasir1","shift_name":"Pagi","opening_cash":500000})
assert r.status_code == 200
sid = r.json()["shift_id"]
print(f"  OK: /api/shifts/open -> #{sid}")

# 8. Active shifts
r = c.get(f"{BASE}/api/shifts/active")
assert r.status_code == 200 and len(r.json()) >= 1
print(f"  OK: /api/shifts/active -> {len(r.json())}")

# 9. Add member
r = c.post(f"{BASE}/api/members", json={"name":"Test","phone":"08123"})
assert r.status_code == 200
mid = r.json()["member_id"]
print(f"  OK: /api/members -> {mid}")

# 10. Lookup member
r = c.get(f"{BASE}/api/members/{mid}")
assert r.status_code == 200 and r.json()["name"] == "Test"
print(f"  OK: /api/members/{mid} -> Test")

# 11. Hold
r = c.post(f"{BASE}/api/hold", json={"items":[{"product_id":1,"qty":1}],"customer_name":"Hold"})
assert r.status_code == 200
hid = r.json()["hold_id"]
print(f"  OK: /api/hold -> {hid}")

# 12. Get holds
r = c.get(f"{BASE}/api/hold")
assert r.status_code == 200 and len(r.json()) >= 1
print(f"  OK: /api/hold (GET) -> {len(r.json())}")

# 13. Checkout
r = c.post(f"{BASE}/api/checkout", json={
    "items": [{"product_id":1,"qty":2,"discount":0,"notes":""},
              {"product_id":3,"qty":3,"discount":0,"notes":""}],
    "payment": "CASH", "discount": 0, "amount_paid": 100000,
    "customer_name": "Customer", "cashier": "kasir1",
    "member_id": mid, "shift_id": sid
})
assert r.status_code == 200
tx = r.json()
assert tx["id"].startswith("TX") and tx["grand_total"] > 0
print(f"  OK: /api/checkout -> {tx['id']} total={tx['grand_total']} change={tx['change']}")

# 14. Stock check
prods = c.get(f"{BASE}/api/products").json()
nasi = [p for p in prods if p["id"]==1][0]
assert nasi["stock"] == 48
print(f"  OK: Stock deducted -> Nasi Goreng={nasi['stock']}")

# 15. Transactions
r = c.get(f"{BASE}/api/transactions")
assert r.status_code == 200 and len(r.json()) == 1
print(f"  OK: /api/transactions -> {len(r.json())}")

# 16. Stats
r = c.get(f"{BASE}/api/stats")
assert r.status_code == 200 and r.json()["total_sales"] > 0
print(f"  OK: /api/stats -> sales={r.json()['total_sales']} tx={r.json()['total_tx']} profit={r.json()['total_profit']}")

# 17. Void
r = c.put(f"{BASE}/api/transactions/{tx['id']}/void")
assert r.status_code == 200
print(f"  OK: /api/transactions/void -> voided")

# 18. Stock restored
prods2 = c.get(f"{BASE}/api/products").json()
nasi2 = [p for p in prods2 if p["id"]==1][0]
assert nasi2["stock"] == 50
print(f"  OK: Stock restored -> {nasi2['stock']}")

# 19. Close shift
r = c.post(f"{BASE}/api/shifts/{sid}/close", json={"closing_cash": 500000})
assert r.status_code == 200 and r.json()["discrepancy"] == 0
print(f"  OK: /api/shifts/close -> disc=0")

# 20. Cash log
r = c.get(f"{BASE}/api/cash/log/{sid}")
assert r.status_code == 200 and len(r.json()) == 2
print(f"  OK: /api/cash/log -> {len(r.json())} entries")

# 21-24. Frontend pages
for p in ["/kasir","/admin","/","/customer"]:
    r = c.get(f"{BASE}{p}")
    assert r.status_code == 200 and "html" in r.headers.get("content-type","")
    print(f"  OK: GET {p} -> HTML")

# 25. Delete hold
r = c.delete(f"{BASE}/api/holds/1")
assert r.status_code == 200
print("  OK: /api/holds/1 -> deleted")

print()
print("========================================")
print("  ALL 25 TESTS PASSED")
print("========================================")
