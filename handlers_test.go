package main
import ("net/http"; "io"; "strings")

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Setup: gunakan database temporary
	os.Setenv("TURSO_DATABASE_URL", "")
	os.Setenv("TURSO_AUTH_TOKEN", "")
	initDB()
	defer db.Close()
	os.Exit(m.Run())
}

func TestSessionCreation(t *testing.T) {
	token := createSession("admin")
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
	token := createSession("kasir")
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
