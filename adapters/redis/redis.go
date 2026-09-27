package redis

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	log "log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/encoding"
)

type client struct {
	conn    *Connection
	isOwner bool
}

// NewClient returns a Cache backed by the default shared Redis connection.
// The underlying connection must have been initialized via package-level setup.
func NewClient(options sop.TransactionOptions) sop.L2Cache {
	log.Debug("NewClient called")
	if options.RedisConfig != nil {
		if options.RedisConfig.URL != "" {
			opts, err := redis.ParseURL(options.RedisConfig.URL)
			if err != nil {
				log.Error("Invalid Redis URL", "url", options.RedisConfig.URL, "error", err)
			} else {
				return NewConnectionClient(Options{
					Address:      opts.Addr,
					Password:     opts.Password,
					DB:           opts.DB,
					TLSConfig:    opts.TLSConfig,
					DialTimeout:  options.RedisConfig.DialTimeout,
					ReadTimeout:  options.RedisConfig.ReadTimeout,
					WriteTimeout: options.RedisConfig.WriteTimeout,
					MaxRetries:   options.RedisConfig.MaxRetries,
				})
			}
		}
		return NewConnectionClient(Options{
			Address:      options.RedisConfig.Address,
			Password:     options.RedisConfig.Password,
			DB:           options.RedisConfig.DB,
			DialTimeout:  options.RedisConfig.DialTimeout,
			ReadTimeout:  options.RedisConfig.ReadTimeout,
			WriteTimeout: options.RedisConfig.WriteTimeout,
			MaxRetries:   options.RedisConfig.MaxRetries,
		})
	}
	// Fallback to default options if no config provided
	return NewConnectionClient(DefaultOptions())
}

// NewConnectionClient opens a new Redis connection with the given options and returns a CloseableCache.
// Call Close on the returned cache when no longer needed. Useful for isolating blob-related caches.
func NewConnectionClient(options Options) sop.CloseableCache {
	log.Info("NewConnectionClient called", "address", options.Address, "db", options.DB)
	c := openConnection(options)
	return &client{
		conn:    c,
		isOwner: true,
	}
}

// Close closes the owned Redis connection, if any.
func (c *client) Close() error {
	if !c.isOwner || c.conn == nil {
		return nil
	}
	log.Info("Closing Redis client connection")
	err := closeConnection(c.conn)
	c.conn = nil
	return err
}

func (c *client) getConnection() (*Connection, error) {
	if c.isOwner {
		if c.conn == nil {
			return nil, fmt.Errorf("redis connection is not open; can't create new client")
		}
		return c.conn, nil
	}
	if connection == nil {
		return nil, fmt.Errorf("redis connection is not open; can't create new client")
	}
	return connection, nil
}

// Returns Redis as L2Cache type.
func (c client) GetType() sop.L2CacheType {
	return sop.Redis
}

// keyNotFound reports whether the provided error corresponds to a missing key in Redis.
func (c client) keyNotFound(err error) bool {
	return err == redis.Nil
}

// Ping tests connectivity to Redis.
func (c client) Ping(ctx context.Context) error {
	conn, err := c.getConnection()
	if err != nil {
		return err
	}
	pong, err := conn.Client.Ping(ctx).Result()
	if err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}
	log.Debug("Redis Ping success", "response", pong)
	fmt.Println(pong, err)
	// Output: PONG <nil>

	return nil
}

// Clear removes all keys in the current Redis database. Use with caution.
func (c client) Clear(ctx context.Context) error {
	log.Warn("Clearing all keys in Redis database")
	conn, err := c.getConnection()
	if err != nil {
		return err
	}
	if err := conn.Client.FlushDB(ctx).Err(); err != nil {
		return fmt.Errorf("redis clear failed: %w", err)
	}
	return nil
}

// Set stores a string value with the specified expiration; expiration < 0 disables caching.
func (c client) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	conn, err := c.getConnection()
	if err != nil {
		return err
	}
	// No caching if expiration < 0.
	if expiration < 0 {
		return nil
	}
	if err := conn.Client.Set(ctx, key, value, expiration).Err(); err != nil {
		return fmt.Errorf("redis set failed for key %s: %w", key, err)
	}
	return nil
}

// Get retrieves a string value. Returns (found, value, error-from-backend).
func (c client) Get(ctx context.Context, key string) (bool, string, error) {
	conn, err := c.getConnection()
	if err != nil {
		return false, "", err
	}
	s, err := conn.Client.Get(ctx, key).Result()
	// Convert key not found into returning false and nil err.
	r := err == nil
	if c.keyNotFound(err) {
		err = nil
	} else if err != nil {
		err = fmt.Errorf("redis get failed for key %s: %w", key, err)
	}
	return r, s, err
}

// GetEx retrieves a string value and sets its expiration (TTL) at the same time.
func (c client) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	conn, err := c.getConnection()
	if err != nil {
		return false, "", err
	}
	s, err := conn.Client.GetEx(ctx, key, expiration).Result()
	// Convert key not found into returning false and nil err.
	r := err == nil
	if c.keyNotFound(err) {
		err = nil
	} else if err != nil {
		err = fmt.Errorf("redis getex failed for key %s: %w", key, err)
	}
	return r, s, err
}

// SetStruct marshals a struct and stores it with the specified expiration; expiration < 0 disables caching.
func (c client) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	conn, err := c.getConnection()
	if err != nil {
		return err
	}

	// No caching if expiration < 0.
	if expiration < 0 {
		return nil
	}

	// serialize User object to JSON
	ba, err := encoding.DefaultMarshaler.Marshal(value)
	if err != nil {
		return fmt.Errorf("redis setstruct marshal failed for key %s: %w", key, err)
	}
	if err := conn.Client.Set(ctx, key, ba, expiration).Err(); err != nil {
		return fmt.Errorf("redis setstruct failed for key %s: %w", key, err)
	}
	return nil
}

// SetStructs upserts multiple struct values under the given keys in a single round trip (pipelined).
func (c client) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	conn, err := c.getConnection()
	if err != nil {
		return err
	}
	if len(keys) != len(values) {
		return fmt.Errorf("keys and values length mismatch")
	}
	// No caching if expiration < 0.
	if expiration < 0 {
		return nil
	}
	if len(keys) == 0 {
		return nil
	}

	pipe := conn.Client.Pipeline()
	for i, key := range keys {
		ba, err := encoding.DefaultMarshaler.Marshal(values[i])
		if err != nil {
			return fmt.Errorf("redis setstructs marshal failed for key %s: %w", key, err)
		}
		pipe.Set(ctx, key, ba, expiration)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis setstructs failed: %w", err)
	}
	return nil
}

// GetStruct retrieves a struct value and unmarshals it into target.
func (c client) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	conn, err := c.getConnection()
	if err != nil {
		return false, err
	}
	if target == nil {
		return false, fmt.Errorf("target can't be nil")
	}
	ba, err := conn.Client.Get(ctx, key).Bytes()
	if err == nil {
		err = encoding.DefaultMarshaler.Unmarshal(ba, target)
		if err != nil {
			err = fmt.Errorf("redis getstruct unmarshal failed for key %s: %w", key, err)
		}
	}

	// Convert key not found into returning false and nil err.
	r := err == nil
	if c.keyNotFound(err) {
		err = nil
	} else if err != nil {
		err = fmt.Errorf("redis getstruct failed for key %s: %w", key, err)
	}
	return r, err
}

// GetStructEx retrieves a struct value with TTL behavior and unmarshals it into target.
func (c client) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	conn, err := c.getConnection()
	if err != nil {
		return false, err
	}
	if target == nil {
		return false, fmt.Errorf("target can't be nil")
	}
	ba, err := conn.Client.GetEx(ctx, key, expiration).Bytes()
	if err == nil {
		err = encoding.DefaultMarshaler.Unmarshal(ba, target)
		if err != nil {
			err = fmt.Errorf("redis getstructex unmarshal failed for key %s: %w", key, err)
		}
	}

	// Convert key not found into returning false and nil err.
	r := err == nil
	if c.keyNotFound(err) {
		err = nil
	} else if err != nil {
		err = fmt.Errorf("redis getstructex failed for key %s: %w", key, err)
	}
	return r, err
}

// GetStructs fetches multiple struct values with optional TTL/sliding expiration semantics.
func (c client) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	if len(keys) != len(targets) {
		return nil, fmt.Errorf("keys and targets length mismatch")
	}
	if len(keys) == 0 {
		return nil, nil
	}
	conn, err := c.getConnection()
	if err != nil {
		return nil, err
	}

	results := make([]bool, len(keys))

	if expiration <= 0 {
		vals, err := conn.Client.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, fmt.Errorf("redis mget failed: %w", err)
		}
		for i, v := range vals {
			if v == nil {
				results[i] = false
				continue
			}
			var ba []byte
			switch s := v.(type) {
			case string:
				ba = []byte(s)
			case []byte:
				ba = s
			default:
				results[i] = false
				continue
			}
			if err := encoding.DefaultMarshaler.Unmarshal(ba, targets[i]); err != nil {
				return nil, fmt.Errorf("unmarshal failed for key %s: %w", keys[i], err)
			}
			results[i] = true
		}
		return results, nil
	}

	pipe := conn.Client.Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.GetEx(ctx, k, expiration)
	}
	_, _ = pipe.Exec(ctx)

	for i, cmd := range cmds {
		err := cmd.Err()
		if err == nil {
			ba, err := cmd.Bytes()
			if err != nil {
				results[i] = false
				continue
			}
			if err := encoding.DefaultMarshaler.Unmarshal(ba, targets[i]); err != nil {
				return nil, fmt.Errorf("unmarshal failed for key %s: %w", keys[i], err)
			}
			results[i] = true
		} else if err == redis.Nil {
			results[i] = false
		} else {
			return nil, fmt.Errorf("pipeline getex failed for key %s: %w", keys[i], err)
		}
	}
	return results, nil
}

// Delete removes keys and returns whether the operation completed without backend errors.
func (c client) Delete(ctx context.Context, keys []string) (bool, error) {
	conn, err := c.getConnection()
	if err != nil {
		return false, err
	}
	var rs = conn.Client.Del(ctx, keys...)

	err = rs.Err()
	// Convert key not found into returning false and nil err.
	r := err == nil
	if c.keyNotFound(err) {
		err = nil
	} else if err != nil {
		err = fmt.Errorf("redis delete failed for keys %v: %w", keys, err)
	}
	return r, err
}

// IsRestarted returns true if the Redis server run_id has changed since the previous call.
// It uses the INFO server section to read run_id, caching it per client instance.
func (c *client) IsRestarted(ctx context.Context) bool {
	return atomic.SwapInt64(&hasRestarted, 0) == 1
}

// IsLockedByOthersTTL checks if the keys are locked by others and extends their TTL if so.
func (c *client) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	if len(lockKeyNames) == 0 {
		return false, nil
	}
	conn, err := c.getConnection()
	if err != nil {
		return false, err
	}
	// Use pipeline to check existence and expire in one go.
	// If EXPIRE returns 1 (true), it existed and TTL was updated.
	pipe := conn.Client.Pipeline()
	cmds := make([]*redis.BoolCmd, len(lockKeyNames))
	for i, k := range lockKeyNames {
		cmds[i] = pipe.Expire(ctx, k, duration)
	}
	_, err = pipe.Exec(ctx)
	if err != nil {
		return false, err
	}
	for _, cmd := range cmds {
		if cmd.Val() {
			return true, nil
		}
	}
	return false, nil
}

func init() {
	sop.RegisterL2CacheFactory(sop.Redis, NewClient)
}
