package config

import (
	"errors"
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

// Load reads config from the environment and errors if the database is unset.
func Load() (Config, error) {
	url, err := databaseURL()
	if err != nil {
		return Config{}, err
	}
	return Config{
		Addr:           getenv("ADDR", ":8080"),
		DatabaseURL:    url,
		RedisAddr:      getenv("REDIS_ADDR", "127.0.0.1:6379"),
		SessionName:    getenv("SESSION_NAME", "session"),
		SessionTTL:     30 * 24 * time.Hour,
		CookieSecure:   getenv("COOKIE_SECURE", "false") == "true",
		CookieSameSite: getenv("COOKIE_SAMESITE", "lax"),
		BcryptCost:     10,
	}, nil
}

// databaseURL returns a DSN from DATABASE_URL or PG* env vars.
// PGUSER/PGDATABASE default to the OS user, as libpq does.
func databaseURL() (string, error) {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v, nil
	}
	host := os.Getenv("PGHOST")
	if host == "" {
		return "", errors.New("neither DATABASE_URL nor PGHOST is set: run inside `devenv shell`, or export DATABASE_URL")
	}
	user := os.Getenv("PGUSER")
	if user == "" {
		user = os.Getenv("USER")
	}
	db := os.Getenv("PGDATABASE")
	if db == "" {
		db = os.Getenv("USER")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	return "host=" + host + " port=" + port + " user=" + user + " dbname=" + db + " sslmode=disable", nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
