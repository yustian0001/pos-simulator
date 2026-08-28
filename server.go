package main

import (
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed frontend/*
var frontendFS embed.FS

//go:embed config.json
var configFile []byte

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	wsMu.Lock()
	wsClients[conn] = true
	wsMu.Unlock()

	defer func() {
		wsMu.Lock()
		delete(wsClients, conn)
		wsMu.Unlock()
		conn.Close()
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func main() {
	initDB()
	defer db.Close()
	go cleanupSessions()

	mux := http.NewServeMux()

	// Auth helper: wrap admin-only endpoints
	adminOnly := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !requireAuth(r, "admin") {
				jsonResponse(w, map[string]string{"error": "Unauthorized"}, 401)
				return
			}
			next(w, r)
		}
	}

	// API routes
	mux.HandleFunc("/api/login", handleLogin)
	mux.HandleFunc("/api/logout", handleLogout)
	mux.HandleFunc("/api/users", adminOnly(handleGetUsers))
	mux.HandleFunc("/api/products", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			handleGetProducts(w, r)
		} else if r.Method == "POST" {
			adminOnly(handleAddProduct)(w, r)
		}
	})
	mux.HandleFunc("/api/products/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			adminOnly(handleUpdateProduct)(w, r)
		} else if r.Method == "DELETE" {
			adminOnly(handleDeleteProduct)(w, r)
		}
	})
	mux.HandleFunc("/api/categories", handleGetCategories)
	mux.HandleFunc("/api/shifts/open", handleOpenShift)
	mux.HandleFunc("/api/shifts/active", handleGetActiveShifts)
	mux.HandleFunc("/api/shifts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			handleOpenShift(w, r)
		} else {
			handleGetShifts(w, r)
		}
	})
	mux.HandleFunc("/api/shifts/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if strings.HasSuffix(r.URL.Path, "/close-self") {
				handleCloseShiftSelf(w, r)
			} else {
				adminOnly(handleCloseShift)(w, r)
			}
		}
	})
	mux.HandleFunc("/api/cash/drop", adminOnly(handleCashDrop))
	mux.HandleFunc("/api/cash/in", adminOnly(handleCashIn))
	mux.HandleFunc("/api/cash/log/", handleGetCashLog)
	mux.HandleFunc("/api/members", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			handleGetMembers(w, r)
		} else if r.Method == "POST" {
			handleAddMember(w, r)
		}
	})
	mux.HandleFunc("/api/members/", handleGetMember)
	mux.HandleFunc("/api/checkout", handleCheckout)
	mux.HandleFunc("/api/hold", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			handleGetHolds(w, r)
		} else if r.Method == "POST" {
			handleHold(w, r)
		}
	})
	mux.HandleFunc("/api/holds/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			handleDeleteHold(w, r)
		}
	})
	mux.HandleFunc("/api/transactions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			handleGetTransactions(w, r)
		}
	})
	mux.HandleFunc("/api/transactions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			handleVoidTransaction(w, r)
		}
	})
	mux.HandleFunc("/api/stats", handleGetStats)
	mux.HandleFunc("/api/sales-trend", handleSalesTrend)
	mux.HandleFunc("/api/payment-breakdown", handlePaymentBreakdown)
	mux.HandleFunc("/api/daily-report", handleDailyReport)
	mux.HandleFunc("/api/stock-report", handleStockReport)
	mux.HandleFunc("/api/e-voucher", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			handleGetEVouchers(w, r)
		} else if r.Method == "POST" {
			handleEVoucher(w, r)
		}
	})
	mux.HandleFunc("/api/quick-access", handleQuickAccess)
	mux.HandleFunc("/api/receipt/", handleReceipt)
	mux.HandleFunc("/api/alerts/low-stock", handleLowStock)
	mux.HandleFunc("/api/backup", adminOnly(handleBackup))
	mux.HandleFunc("/api/restore", adminOnly(handleRestore))
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			handleGetSettings(w, r)
		} else if r.Method == "PUT" {
			adminOnly(handleUpdateSettings)(w, r)
		}
	})
	mux.HandleFunc("/api/ws-broadcast", handleWSBroadcast)
	mux.HandleFunc("/ws", handleWebSocket)
	mux.HandleFunc("/health", handleHealth)

	// Frontend routes (embedded)
	frontendHandler := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			data, err := frontendFS.ReadFile("frontend/" + name)
			if err != nil {
				http.Error(w, "Not found", 404)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			w.Write(data)
		}
	}

	mux.HandleFunc("/kasir", frontendHandler("kasir.html"))
	mux.HandleFunc("/admin", frontendHandler("admin.html"))
	mux.HandleFunc("/customer", frontendHandler("customer.html"))
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		data, _ := frontendFS.ReadFile("frontend/sw.js")
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(data)
	})
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		data, _ := frontendFS.ReadFile("frontend/manifest.json")
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			frontendHandler("index.html")(w, r)
		} else {
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/receipt", frontendHandler("receipt.html"))
	mux.HandleFunc("/admin-login", frontendHandler("admin-login.html"))
	mux.HandleFunc("/admin-dashboard", frontendHandler("admin.html")) // redirect to admin

	port := "8070"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	fmt.Printf("[POS] Server starting on http://localhost:%s/\n", port)
	fmt.Printf("[POS] Data dir: %s\n", getDataDir())
	fmt.Printf("[POS] Version: 2.2 (Go)\n")

	// Auto-open browser
	go func() {
		time.Sleep(2 * time.Second)
		url := "http://localhost:" + port + "/"
		cacheBust := fmt.Sprintf("%d", time.Now().UnixMilli())
		freshURL := url + "?v=" + cacheBust
		fmt.Printf("[POS] Opening browser: %s\n", freshURL)
		if runtime.GOOS == "windows" {
			exec.Command("cmd", "/c", "start", "chrome", "--kiosk-printing", freshURL).Start()
		} else if runtime.GOOS == "darwin" {
			exec.Command("open", freshURL).Start()
		} else {
			exec.Command("xdg-open", freshURL).Start()
		}
	}()

	// Auto-start Cloudflare Tunnel if cloudflared.exe exists
	go func() {
		time.Sleep(3 * time.Second)
		exePath, _ := os.Executable()
		exeDir := filepath.Dir(exePath)
		cloudflared := filepath.Join(exeDir, "cloudflared.exe")
		if _, err := os.Stat(cloudflared); os.IsNotExist(err) {
			return
		}
		fmt.Printf("[POS] Starting Cloudflare Tunnel...\n")
		cmd := exec.Command(cloudflared, "tunnel", "--url", "http://localhost:"+port)
		cmd.Stderr = nil
		var tunnelOutput strings.Builder
		cmd.Stdout = &tunnelOutput
		cmd.Start()

		// Wait and read output
		publicURL := ""
		for i := 0; i < 30; i++ {
			time.Sleep(1 * time.Second)
			out := tunnelOutput.String()
			// Find trycloudflare.com URL
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "https://") && strings.Contains(line, "trycloudflare.com") {
					publicURL = strings.TrimRight(line, " .,;:!?") + "/admin-login"
					break
				}
			}
			if publicURL != "" {
				break
			}
		}

		if publicURL != "" {
			fmt.Printf("[POS] ============================================\n")
			fmt.Printf("[POS] Remote Admin URL:\n")
			fmt.Printf("[POS] %s\n", publicURL)
			fmt.Printf("[POS] Buka link ini dari HP/device lain\n")
			fmt.Printf("[POS] Login: admin / admin123\n")
			fmt.Printf("[POS] ============================================\n")
		} else {
			fmt.Printf("[POS] Tunnel started but URL not found. Check cloudflared output.\n")
		}
	}()

	log.Fatal(http.ListenAndServe(":"+port, mux))
}
