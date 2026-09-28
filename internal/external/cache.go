package external

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// shortTTL caps how long per-client data used on the request form may be
// reused. The specification allows at most 60 seconds.
const shortTTL = 60 * time.Second

// Cached wraps a Directory with small in-memory caches.
//
// The store-wide client list is the expensive query and is cached for a
// configurable TTL; everything shown on the request form is cached only
// briefly, so an admin's change in the external database appears quickly.
type Cached struct {
	inner    Directory
	listTTL  time.Duration
	mu       sync.Mutex
	entries  map[string]*entry
	nowFunc  func() time.Time
	lastSwep time.Time
}

type entry struct {
	value     any
	err       error
	expiresAt time.Time
}

var _ Directory = (*Cached)(nil)

// NewCached wraps inner. listTTL applies to ListClientsByStores; values below
// zero fall back to five minutes.
func NewCached(inner Directory, listTTL time.Duration) *Cached {
	if listTTL <= 0 {
		listTTL = 5 * time.Minute
	}
	return &Cached{
		inner:   inner,
		listTTL: listTTL,
		entries: map[string]*entry{},
		nowFunc: time.Now,
	}
}

// get returns a cached value or calls load and caches the result, including a
// not-found error (so a mistyped code does not hammer the external database).
// Transport failures are never cached.
func (c *Cached) get(key string, ttl time.Duration, load func() (any, error)) (any, error) {
	now := c.nowFunc()

	c.mu.Lock()
	c.sweepLocked(now)
	if e, ok := c.entries[key]; ok && now.Before(e.expiresAt) {
		c.mu.Unlock()
		return e.value, e.err
	}
	c.mu.Unlock()

	// Loading happens outside the lock: a slow external query must not block
	// unrelated lookups. A duplicate concurrent load is cheap and harmless.
	value, err := load()
	if err != nil && err != ErrNotFound {
		return value, err
	}

	c.mu.Lock()
	c.entries[key] = &entry{value: value, err: err, expiresAt: now.Add(ttl)}
	c.mu.Unlock()
	return value, err
}

// sweepLocked discards expired entries at most once a minute. The cache holds
// a few thousand keys at most, so a full scan is cheaper than per-key timers.
func (c *Cached) sweepLocked(now time.Time) {
	if now.Sub(c.lastSwep) < time.Minute {
		return
	}
	c.lastSwep = now
	for k, e := range c.entries {
		if !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}
}

// GetSaler implements Directory.
func (c *Cached) GetSaler(ctx context.Context, login string) (*Saler, error) {
	v, err := c.get("saler\x00"+strings.ToLower(strings.TrimSpace(login)), shortTTL, func() (any, error) {
		return c.inner.GetSaler(ctx, login)
	})
	s, _ := v.(*Saler)
	if s == nil {
		return nil, err
	}
	out := *s
	return &out, err
}

// GetClientByCode implements Directory.
func (c *Cached) GetClientByCode(ctx context.Context, code string) (*Client, error) {
	v, err := c.get("client\x00"+strings.TrimSpace(code), shortTTL, func() (any, error) {
		return c.inner.GetClientByCode(ctx, code)
	})
	cl, _ := v.(*Client)
	if cl == nil {
		return nil, err
	}
	out := *cl
	return &out, err
}

// ListClientLogins implements Directory.
func (c *Cached) ListClientLogins(ctx context.Context, clientCode string) ([]string, error) {
	v, err := c.get("logins\x00"+strings.TrimSpace(clientCode), shortTTL, func() (any, error) {
		return c.inner.ListClientLogins(ctx, clientCode)
	})
	logins, _ := v.([]string)
	return append([]string(nil), logins...), err
}

// ListClientsByStores implements Directory.
func (c *Cached) ListClientsByStores(ctx context.Context, storeValues []string) ([]Client, error) {
	key := make([]string, len(storeValues))
	for i, s := range storeValues {
		key[i] = strings.ToLower(strings.TrimSpace(s))
	}
	sort.Strings(key)

	v, err := c.get("clients\x00"+strings.Join(key, "\x01"), c.listTTL, func() (any, error) {
		return c.inner.ListClientsByStores(ctx, storeValues)
	})
	clients, _ := v.([]Client)
	return append([]Client(nil), clients...), err
}

// Ping implements Directory and is never cached.
func (c *Cached) Ping(ctx context.Context) error { return c.inner.Ping(ctx) }

// Close implements Directory.
func (c *Cached) Close() error { return c.inner.Close() }
