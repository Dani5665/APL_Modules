package auth

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"haynesproform/internal/store"
)

// Login throttling limits.
const (
	MaxLoginFailures = 5
	LoginLockout     = 15 * time.Minute
)

// Throttle counts failed logins per IP and per username and locks either out
// after too many failures.
type Throttle struct {
	db *store.DB
}

// NewThrottle builds a throttle over the local database, so lockouts survive
// a restart.
func NewThrottle(db *store.DB) *Throttle { return &Throttle{db: db} }

func keysFor(ip, username string) map[string]string {
	return map[string]string{"ip": ip, "user": strings.ToLower(strings.TrimSpace(username))}
}

// Locked reports whether this IP or username is currently locked out.
func (t *Throttle) Locked(ctx context.Context, audience, ip, username string) (store.LockState, error) {
	return t.db.LoginLocked(ctx, audience, keysFor(ip, username))
}

// RegisterFailure records a failed attempt.
func (t *Throttle) RegisterFailure(ctx context.Context, audience, ip, username string) error {
	return t.db.RegisterLoginFailure(ctx, audience, keysFor(ip, username), MaxLoginFailures, LoginLockout)
}

// Clear forgets the failures after a successful login.
func (t *Throttle) Clear(ctx context.Context, audience, ip, username string) error {
	return t.db.ClearLoginFailures(ctx, audience, keysFor(ip, username))
}

// ClientIP returns the caller's address.
//
// X-Forwarded-For is honoured only when the immediate peer is one of the
// configured trusted proxies; otherwise any client could forge its own
// address and evade the per-IP lockout.
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	remote := remoteIP(r.RemoteAddr)
	remoteStr := ""
	if remote != nil {
		remoteStr = remote.String()
	}
	if len(trusted) == 0 || !ipInAny(remote, trusted) {
		return remoteStr
	}

	// Walk right to left and take the right-most address that is not itself a
	// trusted proxy: everything to its left was supplied by an untrusted hop.
	forwarded := r.Header.Get("X-Forwarded-For")
	parts := strings.Split(forwarded, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := net.ParseIP(strings.TrimSpace(parts[i]))
		if candidate == nil {
			continue
		}
		if ipInAny(candidate, trusted) {
			continue
		}
		return candidate.String()
	}
	return remoteStr
}

func remoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func ipInAny(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
