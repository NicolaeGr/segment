package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nicolaegr/segment/internal/auth"
	"github.com/nicolaegr/segment/internal/config"
	"github.com/nicolaegr/segment/internal/store"
	"github.com/nicolaegr/segment/internal/web"
	"github.com/nicolaegr/segment/internal/web/seg"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.RedisAddr)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer st.Close()

	if err := store.Migrate(ctx, st.PG); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := store.Seed(ctx, st.PG, cfg.BcryptCost); err != nil {
		log.Fatalf("seed: %v", err)
	}

	// The segment cache moves to Redis (swapping the in-memory default).
	seg.DefaultCache = store.NewRedisCache(st.Redis)
	sessions := auth.NewSessionManager(auth.SessionConfig{
		Redis:    st.Redis,
		Name:     cfg.SessionName,
		TTL:      cfg.SessionTTL,
		Secure:   cfg.CookieSecure,
		SameSite: cfg.CookieSameSite,
	})
	users := store.NewUsers(st.PG)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           web.New(web.Deps{Store: st, Sessions: sessions, Users: users}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("listening on http://localhost%s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
