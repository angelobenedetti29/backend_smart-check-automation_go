package guard

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type bucket struct { tokens float64; at time.Time }

// Limiter is a bounded per-process token bucket.
type Limiter struct { mu sync.Mutex; entries map[string]bucket; rate, capacity float64; maxEntries int; idleFor time.Duration }

// NewLimiter creates a limiter. Callers may use Allow with a trusted identity
// (for example a verified device UUID) rather than the transport peer.
func NewLimiter(ratePerSecond float64, burst int) *Limiter { return &Limiter{entries: map[string]bucket{}, rate: ratePerSecond, capacity: float64(burst), maxEntries: 4096, idleFor: 10 * time.Minute} }

// Allow consumes one token for class/key and evicts expired/old entries.
func (l *Limiter) Allow(class, key string) bool {
	now := time.Now(); storageKey := class + "|" + key
	l.mu.Lock(); defer l.mu.Unlock()
	for staleKey, stale := range l.entries { if now.Sub(stale.at) > l.idleFor { delete(l.entries, staleKey) } }
	if _, exists := l.entries[storageKey]; !exists && len(l.entries) >= l.maxEntries { var oldest string; var oldestAt time.Time; for candidate, entry := range l.entries { if oldest == "" || entry.at.Before(oldestAt) { oldest, oldestAt = candidate, entry.at } }; if oldest != "" { delete(l.entries, oldest) } }
	b := l.entries[storageKey]; if b.at.IsZero() { b.at = now; b.tokens = l.capacity }; b.tokens += now.Sub(b.at).Seconds()*l.rate; if b.tokens > l.capacity { b.tokens = l.capacity }; b.at = now; if b.tokens < 1 { l.entries[storageKey] = b; return false }; b.tokens--; l.entries[storageKey] = b; return true
}

// Middleware applies a peer-keyed rate limit. Forwarded headers are ignored.
func (l *Limiter) Middleware(classify func(*http.Request) string, next http.Handler) http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { class := classify(r); if class == "" { next.ServeHTTP(w, r); return }; if !l.Allow(class, peer(r)) { rateLimited(w); return }; next.ServeHTTP(w, r) }) }

// RateLimited writes the stable JSON 429 response.
func RateLimited(w http.ResponseWriter) { rateLimited(w) }
func rateLimited(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json"); w.Header().Set("Retry-After", "60"); w.WriteHeader(http.StatusTooManyRequests); _, _ = w.Write([]byte(`{"success":false,"message":"Demasiadas solicitudes","errors":{"code":"rate_limited"}}`)) }
func peer(r *http.Request) string { host, _, err := net.SplitHostPort(r.RemoteAddr); if err == nil { return host }; return strings.TrimSpace(r.RemoteAddr) }
