package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"haynesproform/internal/dates"
	"haynesproform/internal/store"
)

// Audience describes one of the two independent login areas. Customer and
// admin sessions use different cookie names and paths, so logging into one
// never grants the other.
type Audience struct {
	Subject    store.SubjectType
	CookieName string
	CookiePath string
	// Name is used as the throttling audience and in audit entries.
	Name string
}

// The two audiences of the application.
var (
	UserAudience = Audience{
		Subject:    store.SubjectUser,
		CookieName: "hpf_session",
		CookiePath: "/",
		Name:       "user",
	}
	AdminAudience = Audience{
		Subject:    store.SubjectAdmin,
		CookieName: "hpf_admin_session",
		CookiePath: "/admin",
		Name:       "admin",
	}
)

// ErrNoSession means the request carries no usable session.
var ErrNoSession = errors.New("auth: no session")

// Manager creates and validates server-side sessions.
type Manager struct {
	db       *store.DB
	idle     time.Duration
	absolute time.Duration
	secure   bool
}

// NewManager builds a session manager. secure controls the Secure cookie
// attribute, which is turned off only for local development over plain HTTP.
func NewManager(db *store.DB, idle, absolute time.Duration, secure bool) *Manager {
	return &Manager{db: db, idle: idle, absolute: absolute, secure: secure}
}

// IdleTimeout returns the configured idle window.
func (m *Manager) IdleTimeout() time.Duration { return m.idle }

// Start creates a session for a subject and sets the cookie. stage is
// store.StageActive, or store.StageAwaitingTOTP for an admin who still owes a
// second factor.
func (m *Manager) Start(ctx context.Context, w http.ResponseWriter, aud Audience, subjectID int64, stage string) (*store.Session, error) {
	token, err := NewToken()
	if err != nil {
		return nil, err
	}
	csrf, err := NewToken()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	s := store.Session{
		TokenHash:   HashToken(token),
		SubjectType: aud.Subject,
		SubjectID:   subjectID,
		CSRFToken:   csrf,
		Stage:       stage,
		CreatedAt:   dates.FormatUTC(now),
		LastSeenAt:  dates.FormatUTC(now),
		ExpiresAt:   dates.FormatUTC(now.Add(m.absolute)),
	}
	if err := m.db.CreateSession(ctx, s); err != nil {
		return nil, err
	}

	m.setCookie(w, aud, token, m.absolute)
	return &s, nil
}

// Rotate replaces the session token while keeping the subject, which is what
// makes a privilege change (password login, then TOTP) safe against session
// fixation.
func (m *Manager) Rotate(ctx context.Context, w http.ResponseWriter, r *http.Request, aud Audience, stage string) (*store.Session, error) {
	old, err := m.Load(ctx, r, aud)
	if err != nil {
		return nil, err
	}
	if err := m.db.DeleteSession(ctx, old.TokenHash); err != nil {
		return nil, err
	}
	return m.Start(ctx, w, aud, old.SubjectID, stage)
}

// Load returns the session referenced by the request cookie, enforcing both
// the idle and absolute timeouts.
func (m *Manager) Load(ctx context.Context, r *http.Request, aud Audience) (*store.Session, error) {
	c, err := r.Cookie(aud.CookieName)
	if err != nil || c.Value == "" {
		return nil, ErrNoSession
	}

	s, err := m.db.SessionByHash(ctx, HashToken(c.Value))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	if s.SubjectType != aud.Subject {
		return nil, ErrNoSession
	}

	lastSeen, err := dates.ParseUTC(s.LastSeenAt)
	if err != nil || time.Since(lastSeen) > m.idle {
		_ = m.db.DeleteSession(ctx, s.TokenHash)
		return nil, ErrNoSession
	}

	// Writing on every request would mean a write per page view; a minute of
	// granularity is enough for an idle timeout measured in hours.
	if time.Since(lastSeen) > time.Minute {
		if err := m.db.TouchSession(ctx, s.TokenHash, time.Now()); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Destroy deletes the current session and clears its cookie.
func (m *Manager) Destroy(ctx context.Context, w http.ResponseWriter, r *http.Request, aud Audience) error {
	if c, err := r.Cookie(aud.CookieName); err == nil && c.Value != "" {
		if err := m.db.DeleteSession(ctx, HashToken(c.Value)); err != nil {
			return err
		}
	}
	m.clearCookie(w, aud)
	return nil
}

func (m *Manager) setCookie(w http.ResponseWriter, aud Audience, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     aud.CookieName,
		Value:    value,
		Path:     aud.CookiePath,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) clearCookie(w http.ResponseWriter, aud Audience) {
	http.SetCookie(w, &http.Cookie{
		Name:     aud.CookieName,
		Value:    "",
		Path:     aud.CookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// EntryCookieName holds the pre-session token that ties a parked entry link
// to the browser that followed it.
const EntryCookieName = "hpf_entry"

// entryCookieTTL bounds how long a parked entry link stays usable.
const entryCookieTTL = 30 * time.Minute

// ParkEntryLink stores an entry link against a fresh pre-session cookie.
func (m *Manager) ParkEntryLink(ctx context.Context, w http.ResponseWriter, clientCode, salerLogin string) error {
	token, err := NewToken()
	if err != nil {
		return err
	}
	if err := m.db.CreatePendingEntryLink(ctx, HashToken(token), clientCode, salerLogin,
		time.Now().Add(entryCookieTTL)); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     EntryCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(entryCookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// PeekEntryLink returns the parked entry link without consuming it.
func (m *Manager) PeekEntryLink(ctx context.Context, r *http.Request) (*store.PendingEntryLink, bool) {
	c, err := r.Cookie(EntryCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	l, err := m.db.PeekPendingEntryLink(ctx, HashToken(c.Value))
	if err != nil {
		return nil, false
	}
	return l, true
}

// TakeEntryLink returns the parked entry link and consumes it.
func (m *Manager) TakeEntryLink(ctx context.Context, w http.ResponseWriter, r *http.Request) (*store.PendingEntryLink, bool) {
	c, err := r.Cookie(EntryCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	l, err := m.db.TakePendingEntryLink(ctx, HashToken(c.Value))
	m.ClearEntryCookie(w)
	if err != nil {
		return nil, false
	}
	return l, true
}

// ClearEntryCookie removes the pre-session cookie.
func (m *Manager) ClearEntryCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     EntryCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
