//go:build db_redis || db_all

package dbstep

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/redis/go-redis/v9"

	"artemis/pkg/shared/models"
)

// Redis takes a command as its argv, the words redis-cli takes:
//
//	db cache { command = ["GET", "session:${sid}"] }
//	expect result == "active"
//
// The reply is `result`: a string, a number, an array, an object for a map
// reply, and null for a key that is not there -- which is an answer, not an
// error. `rows` is [] and `affected` null: a reply is not rows.
type redisDriver struct{}

func init() {
	Register("redis", redisDriver{})
	Register("rediss", redisDriver{})
}

func (redisDriver) Open(ctx context.Context, rawURL string) (Conn, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("redis: the URL does not parse: %s", Redact(rawURL))
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("connecting to %s: %w", Redact(rawURL), err)
	}
	return &redisConn{client: client}, nil
}

type redisConn struct{ client *redis.Client }

func (c *redisConn) Close() error { return c.client.Close() }

func (c *redisConn) SQL(context.Context, Statement, []any) (Result, error) {
	return Result{}, errors.New(`redis takes a command, not sql: command = ["GET", "key"]`)
}

// Command sends cmd, an argv, and reads its reply.
func (c *redisConn) Command(ctx context.Context, cmd any) (Result, error) {
	argv, ok := cmd.([]any)
	if !ok || len(argv) == 0 {
		return Result{}, errors.New(`redis: a command is its words as an array: command = ["GET", "key"]`)
	}
	args := make([]any, len(argv))
	for i, a := range argv {
		switch t := a.(type) {
		case models.FileArg:
			b, err := os.ReadFile(string(t))
			if err != nil {
				return Result{}, fmt.Errorf("redis: command[%d]: %w", i, err)
			}
			args[i] = b
		case float64:
			if t == math.Trunc(t) && math.Abs(t) <= maxExact {
				args[i] = int64(t)
			} else {
				args[i] = t
			}
		case string, bool:
			args[i] = t
		case nil:
			return Result{}, fmt.Errorf("redis: command[%d] is null, and a command's words are text", i)
		default:
			return Result{}, fmt.Errorf("redis: command[%d] is %s; send an object as to_json(...)", i, describe(t))
		}
	}
	reply, err := c.client.Do(ctx, args...).Result()
	if errors.Is(err, redis.Nil) {
		return Result{Rows: []any{}}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("redis: %w", err)
	}
	return Result{Rows: []any{}, Value: fromRedis(reply)}, nil
}

// fromRedis is a reply in the evaluator's domain.
func fromRedis(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string, bool, float64:
		return t
	case int64:
		return whole(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = fromRedis(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprint(k)] = fromRedis(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = fromRedis(e)
		}
		return out
	case []byte:
		return string(t)
	case error:
		// An error inside an array reply -- one command of a MULTI that
		// failed -- is its message.
		return t.Error()
	}
	return fmt.Sprint(v)
}

// describe names what a value is, for an error.
func describe(v any) string {
	switch v.(type) {
	case models.Doc, map[string]any:
		return "an object"
	case []any:
		return "an array"
	}
	return fmt.Sprintf("a %T", v)
}
