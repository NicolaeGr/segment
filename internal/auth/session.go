// Package auth provides cookie sessions backed by Redis: an opaque token in
// an HttpOnly cookie maps to a user id in Redis with a TTL.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

type SessionManager struct {
	rdb       *redis.Client
	name      string
	ttl       time.Duration
	secure    bool
	sameSite  http.SameSite
	keyPrefix string
}

type SessionConfig struct {
	Redis    *redis.Client
	Name     string
	TTL      time.Duration
	Secure   bool
	SameSite string // "lax", "strict", "none"
}

func NewSessionManager(cfg SessionConfig) *SessionManager {
	sameSite := http.SameSiteLaxMode
	switch cfg.SameSite {
	case "strict":
		sameSite = http.SameSiteStrictMode
	case "none":
		sameSite = http.SameSiteNoneMode
	}
	return &SessionManager{
		rdb:       cfg.Redis,
		name:      cfg.Name,
		ttl:       cfg.TTL,
		secure:    cfg.Secure,
		sameSite:  sameSite,
		keyPrefix: "session:",
	}
}

var ErrNoSession = errors.New("auth: no session")

func (m *SessionManager) Create(w http.ResponseWriter, userID int64) error {
	token, err := newToken()
	if err != nil {
		return err
	}
	key := m.keyPrefix + token
	if err := m.rdb.Set(context.Background(), key, userID, m.ttl).Err(); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.name,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: m.sameSite,
		MaxAge:   int(m.ttl.Seconds()),
	})
	return nil
}

func (m *SessionManager) UserID(r *http.Request) (int64, error) {
	c, err := r.Cookie(m.name)
	if err != nil || c.Value == "" {
		return 0, ErrNoSession
	}
	key := m.keyPrefix + c.Value
	val, err := m.rdb.Get(context.Background(), key).Int64()
	if err == redis.Nil {
		return 0, ErrNoSession
	}
	if err != nil {
		return 0, err
	}
	return val, nil
}

func (m *SessionManager) Destroy(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(m.name); err == nil && c.Value != "" {
		_ = m.rdb.Del(context.Background(), m.keyPrefix+c.Value).Err()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: m.sameSite,
		MaxAge:   -1,
	})
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type ctxKey int

const userIDKey ctxKey = 0

func WithUserID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

func UserIDFrom(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(userIDKey).(int64)
	return id, ok
}

// RequireAuth protects a route tree. htmx fragment requests get an
// HX-Redirect header (full client navigation); plain loads get a 303.
func (m *SessionManager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, err := m.UserID(r)
		if err != nil {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), uid)))
	})
}
