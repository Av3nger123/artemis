package dbstep

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"artemis/pkg/shared/models"
)

// These run against real servers, and only when one is named:
//
//	ARTEMIS_TEST_MONGO_URL=mongodb://app:pw@localhost:27017/shop?authSource=admin
//	ARTEMIS_TEST_REDIS_URL=redis://:pw@localhost:6379/0

func TestToBSONKeepsOrderAndReadsExtendedJSON(t *testing.T) {
	cmd := models.Doc{
		{Key: "find", Value: "orders"},
		{Key: "filter", Value: models.Doc{
			{Key: "_id", Value: models.Doc{{Key: "$oid", Value: "65a1b2c3d4e5f60718293a4b"}}},
			{Key: "at", Value: models.Doc{{Key: "$date", Value: "2026-10-26T09:30:15Z"}}},
		}},
		{Key: "sort", Value: models.Doc{{Key: "b", Value: 1.0}, {Key: "a", Value: -1.0}}},
		{Key: "limit", Value: 5.0},
		{Key: "ratio", Value: 0.5},
	}
	got, err := toBSON(cmd)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := bson.ObjectIDFromHex("65a1b2c3d4e5f60718293a4b")
	want := bson.D{
		{Key: "find", Value: "orders"},
		{Key: "filter", Value: bson.D{
			{Key: "_id", Value: id},
			{Key: "at", Value: bson.NewDateTimeFromTime(time.Date(2026, 10, 26, 9, 30, 15, 0, time.UTC))},
		}},
		{Key: "sort", Value: bson.D{{Key: "b", Value: int32(1)}, {Key: "a", Value: int32(-1)}}},
		{Key: "limit", Value: int32(5)},
		{Key: "ratio", Value: 0.5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("toBSON =\n%#v\nwant\n%#v", got, want)
	}
	if _, err := toBSON(models.Doc{{Key: "$oid", Value: "nope"}}); err == nil {
		t.Error("a bad $oid was accepted")
	}
}

func TestFromBSON(t *testing.T) {
	id, _ := bson.ObjectIDFromHex("65a1b2c3d4e5f60718293a4b")
	dec, _ := bson.ParseDecimal128("12.50")
	got := fromBSON(bson.D{
		{Key: "_id", Value: id},
		{Key: "n", Value: int32(3)},
		{Key: "big", Value: int64(1) << 60},
		{Key: "total", Value: dec},
		{Key: "at", Value: bson.NewDateTimeFromTime(time.Date(2026, 10, 26, 9, 30, 15, 0, time.UTC))},
		{Key: "tags", Value: bson.A{"a", nil}},
	})
	want := map[string]any{
		"_id": "65a1b2c3d4e5f60718293a4b", "n": 3.0, "big": "1152921504606846976", "total": 12.5,
		"at": "2026-10-26T09:30:15Z", "tags": []any{"a", nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fromBSON = %#v\nwant %#v", got, want)
	}
}

func TestMongo(t *testing.T) {
	url := os.Getenv("ARTEMIS_TEST_MONGO_URL")
	if url == "" {
		t.Skip("ARTEMIS_TEST_MONGO_URL is not set")
	}
	p := NewPools()
	defer p.Close()
	ctx := WithPools(context.Background(), p)
	run := func(cmd models.Doc) map[string]any {
		t.Helper()
		roots, err := Executor{}.Observe(ctx, models.Step{Type: StepType, DB: models.DB{URL: url, Command: cmd}}, nil)
		if err != nil {
			t.Fatalf("%v: %v", cmd, err)
		}
		return roots
	}
	run(models.Doc{{Key: "drop", Value: "orders"}}) // a missing collection drops cleanly since MongoDB 7
	defer run(models.Doc{{Key: "drop", Value: "orders"}})

	docs := make([]any, 0, 150)
	for i := 0; i < 150; i++ {
		docs = append(docs, models.Doc{{Key: "n", Value: float64(i)}, {Key: "status", Value: "paid"}})
	}
	ins := run(models.Doc{{Key: "insert", Value: "orders"}, {Key: "documents", Value: docs}})
	if ins[RootAffected] != 150.0 {
		t.Errorf("insert affected = %v, want 150 (its n)", ins[RootAffected])
	}
	if ins[RootResult].(map[string]any)["ok"] != 1.0 {
		t.Errorf("insert result = %v, want ok: 1", ins[RootResult])
	}

	// 150 documents is past a first batch of 101: every one is read.
	found := run(models.Doc{{Key: "find", Value: "orders"}, {Key: "filter", Value: models.Doc{{Key: "status", Value: "paid"}}},
		{Key: "sort", Value: models.Doc{{Key: "n", Value: -1.0}}}})
	rows := found[RootRows].([]any)
	if len(rows) != 150 || rows[0].(map[string]any)["n"] != 149.0 {
		t.Fatalf("find returned %d rows, first %v; want 150, sorted n descending", len(rows), rows[0])
	}
	id := rows[0].(map[string]any)["_id"].(string)

	byID := run(models.Doc{{Key: "find", Value: "orders"}, {Key: "filter", Value: models.Doc{{Key: "_id", Value: models.Doc{{Key: "$oid", Value: id}}}}}})
	if len(byID[RootRows].([]any)) != 1 {
		t.Errorf("a find by {$oid} returned %v", byID[RootRows])
	}

	agg := run(models.Doc{{Key: "aggregate", Value: "orders"}, {Key: "pipeline", Value: []any{
		models.Doc{{Key: "$group", Value: models.Doc{{Key: "_id", Value: "$status"}, {Key: "total", Value: models.Doc{{Key: "$sum", Value: "$n"}}}}}},
	}}, {Key: "cursor", Value: models.Doc{}}})
	if r := agg[RootRows].([]any); len(r) != 1 || r[0].(map[string]any)["total"] != 11175.0 {
		t.Errorf("aggregate = %v, want one group totalling 11175", r)
	}

	del := run(models.Doc{{Key: "delete", Value: "orders"}, {Key: "deletes", Value: []any{
		models.Doc{{Key: "q", Value: models.Doc{{Key: "n", Value: models.Doc{{Key: "$lt", Value: 10.0}}}}}, {Key: "limit", Value: 0.0}},
	}}})
	if del[RootAffected] != 10.0 {
		t.Errorf("delete affected = %v, want 10", del[RootAffected])
	}

	_, err := Executor{}.Observe(ctx, models.Step{Type: StepType, DB: models.DB{URL: url, SQL: "SELECT 1"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "takes a command") {
		t.Errorf("sql against mongodb: %v", err)
	}
}

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
