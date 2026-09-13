package guard

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterIndependentKeysAndBoundedEviction(t *testing.T) {
	l := NewLimiter(0, 1)
	l.maxEntries = 2
	if !l.Allow("device", "uuid-a") {
		t.Fatal("first identity should receive its burst")
	}
	if !l.Allow("device", "uuid-b") {
		t.Fatal("second identity should have an independent quota")
	}
	if len(l.entries) != 2 {
		t.Fatalf("entries=%d want 2", len(l.entries))
	}
	if !l.Allow("device", "uuid-c") {
		t.Fatal("evicted identity should receive a fresh burst")
	}
	if len(l.entries) != 2 {
		t.Fatalf("entries grew beyond bound: %d", len(l.entries))
	}
}

func TestLimiterExpiresIdleEntriesAndReturnsJSON429(t *testing.T) {
	l := NewLimiter(0, 1)
	l.idleFor = time.Millisecond
	if !l.Allow("peer", "one") {
		t.Fatal("expected initial token")
	}
	time.Sleep(3 * time.Millisecond)
	if !l.Allow("peer", "127.0.0.1") {
		t.Fatal("new peer should receive a fresh burst")
	}
	called := false
	h := l.Middleware(func(*http.Request) string { return "peer" }, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if called || rw.Code != http.StatusTooManyRequests || rw.Header().Get("Retry-After") == "" {
		t.Fatalf("expected JSON 429 with Retry-After: called=%v status=%d", called, rw.Code)
	}
}
