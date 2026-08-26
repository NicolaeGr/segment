package config

import (
	"os"
	"time"
)

type Config struct {
	Addr string

	DatabaseURL string
	RedisAddr   string

	SessionName    string
	SessionTTL     time.Duration
	CookieSecure   bool
	CookieSameSite string

	BcryptCost int
}

func Load() Config {
	return Config{
		Addr:           getenv("ADDR", ":8080"),
		DatabaseURL:    databaseURL(),
		RedisAddr:      getenv("REDIS_ADDR", "127.0.0.1:6379"),
		SessionName:    getenv("SESSION_NAME", "session"),
		SessionTTL:     30 * 24 * time.Hour,
		CookieSecure:   getenv("COOKIE_SECURE", "false") == "true",
		CookieSameSite: getenv("COOKIE_SAMESITE", "lax"),
		BcryptCost:     10,
	}
}

// databaseURL builds a keyword/value DSN from PG* env vars (devenv sets them)
// so a unix socket directory path needs no escaping.
func databaseURL() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	host := getenv("PGHOST", "/run/user/1000/devenv-386ec41/postgres")
	port := getenv("PGPORT", "5432")
	user := getenv("PGUSER", os.Getenv("USER"))
	db := getenv("PGDATABASE", os.Getenv("USER"))
	return "host=" + host + " port=" + port + " user=" + user + " dbname=" + db + " sslmode=disable"
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
