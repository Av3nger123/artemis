//go:build db_redis || db_all

package dbstep

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"artemis/pkg/shared/models"
)

// TestRedis runs against a real server, and only when one is named:
//
//	ARTEMIS_TEST_REDIS_URL=redis://:pw@localhost:6379/0

func TestRedis(t *testing.T) {
	url := os.Getenv("ARTEMIS_TEST_REDIS_URL")
	if url == "" {
		t.Skip("ARTEMIS_TEST_REDIS_URL is not set")
	}
	p := NewPools()
	defer p.Close()
	ctx := WithPools(context.Background(), p)
	run := func(argv ...any) any {
		t.Helper()
		roots, err := Executor{}.Observe(ctx, models.Step{Type: StepType, DB: models.DB{URL: url, Command: argv}}, nil)
		if err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		return roots[RootResult]
	}
	run("DEL", "artemis:s", "artemis:h", "artemis:l", "artemis:n")
	if got := run("SET", "artemis:s", "active"); got != "OK" {
		t.Errorf("SET = %v", got)
	}
	if got := run("GET", "artemis:s"); got != "active" {
		t.Errorf("GET = %v", got)
	}
	if got := run("GET", "artemis:missing"); got != nil {
		t.Errorf("GET of a missing key = %#v, want null", got)
	}
	if got := run("INCRBY", "artemis:n", 5.0); got != 5.0 {
		t.Errorf("INCRBY = %#v, want 5", got)
	}
	run("HSET", "artemis:h", "a", "1", "b", "2")
	if got := run("HGETALL", "artemis:h"); !reflect.DeepEqual(got, map[string]any{"a": "1", "b": "2"}) {
		t.Errorf("HGETALL = %#v", got)
	}
	run("RPUSH", "artemis:l", "x", "y")
	if got := run("LRANGE", "artemis:l", 0.0, -1.0); !reflect.DeepEqual(got, []any{"x", "y"}) {
		t.Errorf("LRANGE = %#v", got)
	}
	_, err := Executor{}.Observe(ctx, models.Step{Type: StepType, DB: models.DB{URL: url, Command: []any{"LPUSH", "artemis:s", "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Errorf("LPUSH on a string: %v, want WRONGTYPE", err)
	}
	run("DEL", "artemis:s", "artemis:h", "artemis:l", "artemis:n")
}
