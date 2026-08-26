package store

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"

	"example.com/segments/internal/web/seg"
)

// RedisCache implements seg.Cache on Redis: values are stored as JSON with a
// TTL. When the segment provides a Decode func, the typed value is rebuilt
// from the stored bytes; otherwise the raw JSON is returned as-is.
type RedisCache struct {
	rdb *redis.Client
}

func NewRedisCache(rdb *redis.Client) *RedisCache {
	return &RedisCache{rdb: rdb}
}

func (c *RedisCache) Get(ctx context.Context, key string, seg seg.Segment) (any, error) {
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if err == nil {
		if seg.Decode != nil {
			if v, derr := seg.Decode(raw); derr == nil {
				return v, nil
			}
		} else {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				return v, nil
			}
		}
	}

	val, err := seg.Load(ctx)
	if err != nil {
		return nil, err
	}
	if seg.TTL > 0 {
		if b, err := json.Marshal(val); err == nil {
			c.rdb.Set(ctx, key, b, seg.TTL)
		}
	}
	return val, nil
}

func (c *RedisCache) Invalidate(segID, sessionID string) {
	c.rdb.Del(context.Background(), "u:"+sessionID+":"+segID)
}

func (c *RedisCache) InvalidateGlobal(segID string) {
	c.rdb.Del(context.Background(), "g:"+segID)
}
