package fbhttp

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// attemptLimiter counts failed attempts per key and blocks a key that fails
// too often within a time window.
type attemptLimiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu       sync.Mutex
	attempts map[string]*attempts
}

type attempts struct {
	count int
	since time.Time
}

func newAttemptLimiter(maxFailures int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{max: maxFailures, window: window, now: time.Now, attempts: map[string]*attempts{}}
}

// sharePasswordAttempts limits wrong share passwords to 10 a minute per link
// and address.
var sharePasswordAttempts = newAttemptLimiter(10, time.Minute)

// current returns the key's attempts in the running window, dropping stale
// entries as it goes so the map can't grow without bound.
func (l *attemptLimiter) current(key string) *attempts {
	now := l.now()
	for k, a := range l.attempts {
		if now.Sub(a.since) >= l.window {
			delete(l.attempts, k)
		}
	}
	return l.attempts[key]
}

func (l *attemptLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.current(key)
	return a != nil && a.count >= l.max
}

func (l *attemptLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a := l.current(key); a != nil {
		a.count++
		return
	}
	l.attempts[key] = &attempts{count: 1, since: l.now()}
}

func (l *attemptLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

// clientAddr is the address a request came from. It deliberately ignores
// X-Forwarded-For and similar headers, which a client can set to anything.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
