package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	PG    *pgxpool.Pool
	Redis *redis.Client
}

func Open(ctx context.Context, databaseURL, redisAddr string) (*Store, error) {
	pg, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pg.Ping(ctx); err != nil {
		return nil, err
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &Store{PG: pg, Redis: rdb}, nil
}

func (s *Store) Close() {
	s.PG.Close()
	if err := s.Redis.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
	}
}

func (s *Store) WaitForRedis(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := s.Redis.Ping(ctx).Err(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return lastErr
}
