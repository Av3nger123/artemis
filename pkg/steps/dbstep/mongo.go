//go:build db_mongodb || db_all

package dbstep

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"

	"artemis/pkg/shared/models"
)

// MongoDB takes a command document -- the same one `db.runCommand()` takes in
// mongosh -- run against the database the URL names:
//
//	db mongo { command = {"find": "orders", "filter": {"status": "paid"}} }
//
// A command that answers with a cursor -- find, aggregate, listCollections,
// listIndexes -- is read to its end, so `rows` is every document it returned
// and not the first batch of 101; any other command's reply is `result`, and
// its `n`, where it has one, is `affected`.
type mongoDriver struct{}

func init() {
	Register("mongodb", mongoDriver{})
	Register("mongodb+srv", mongoDriver{})
}

// cursorCommands are the commands whose reply is a cursor over documents.
var cursorCommands = map[string]bool{
	"find": true, "aggregate": true, "listCollections": true, "listIndexes": true,
}

func (mongoDriver) Open(ctx context.Context, rawURL string) (Conn, error) {
	cs, err := connstring.ParseAndValidate(rawURL)
	if err != nil {
		return nil, fmt.Errorf("mongodb: the URL does not parse: %s", Redact(rawURL))
	}
	if cs.Database == "" {
		return nil, fmt.Errorf("mongodb: %s names no database; add it as the path: mongodb://host:27017/shop", Redact(rawURL))
	}
	client, err := mongo.Connect(options.Client().ApplyURI(rawURL))
	if err != nil {
		return nil, fmt.Errorf("mongodb: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(context.Background()) //nolint:errcheck // the ping's error is the one worth reporting
		return nil, fmt.Errorf("connecting to %s: %w", Redact(rawURL), err)
	}
	return &mongoConn{client: client, db: client.Database(cs.Database)}, nil
}

type mongoConn struct {
	client *mongo.Client
	db     *mongo.Database
}

func (c *mongoConn) Close() error { return c.client.Disconnect(context.Background()) }

func (c *mongoConn) SQL(context.Context, Statement, []any) (Result, error) {
	return Result{}, errors.New(`mongodb takes a command, not sql: command = {"find": "orders", "filter": {...}}`)
}

// Command runs cmd, a command document whose first key names the command.
func (c *mongoConn) Command(ctx context.Context, cmd any) (Result, error) {
	doc, err := toBSON(cmd)
	if err != nil {
		return Result{}, fmt.Errorf("mongodb: %w", err)
	}
	d, ok := doc.(bson.D)
	if !ok || len(d) == 0 {
		return Result{}, errors.New(`mongodb: a command is a document, its first key the command: command = {"find": "orders"}`)
	}
	if cursorCommands[d[0].Key] {
		return c.cursor(ctx, d)
	}
	var reply bson.D
	if err := c.db.RunCommand(ctx, d).Decode(&reply); err != nil {
		return Result{}, fmt.Errorf("mongodb: %w", err)
	}
	value := fromBSON(reply).(map[string]any)
	out := Result{Rows: []any{}, Value: value}
	if n, ok := value["n"].(float64); ok {
		out.Affected = &n
	}
	return out, nil
}

// cursor reads every document a cursor command returns, through as many
// getMore round trips as it takes, up to MaxRows.
func (c *mongoConn) cursor(ctx context.Context, cmd bson.D) (Result, error) {
	cur, err := c.db.RunCommandCursor(ctx, cmd)
	if err != nil {
		return Result{}, fmt.Errorf("mongodb: %w", err)
	}
	defer cur.Close(context.Background())
	out := Result{Rows: []any{}}
	for cur.Next(ctx) {
		if len(out.Rows) == MaxRows {
			return Result{}, fmt.Errorf("the command returned more than %d documents; add a limit, or count them", MaxRows)
		}
		var doc bson.D
		if err := cur.Decode(&doc); err != nil {
			return Result{}, fmt.Errorf("mongodb: %w", err)
		}
		out.Rows = append(out.Rows, fromBSON(doc))
	}
	if err := cur.Err(); err != nil {
		return Result{}, fmt.Errorf("mongodb: %w", err)
	}
	n := float64(len(out.Rows))
	out.Affected = &n
	return out, nil
}

// toBSON is a command as the driver sends it. An object written out keeps its
// key order; an object with one key from MongoDB Extended JSON -- {"$oid":
// "..."}, {"$date": "..."}, {"$numberDecimal": "..."}, {"$numberLong": "..."}
// -- is that BSON type, which is how a filter names an ObjectId. A whole number
// that fits is an int32 and any other number a double, as mongosh and the
// Node driver send them.
func toBSON(v any) (any, error) {
	switch t := v.(type) {
	case models.Doc:
		if len(t) == 1 {
			if x, ok, err := extended(t[0].Key, t[0].Value); ok {
				return x, err
			}
		}
		d := make(bson.D, 0, len(t))
		for _, f := range t {
			x, err := toBSON(f.Value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Key, err)
			}
			d = append(d, bson.E{Key: f.Key, Value: x})
		}
		return d, nil
	case map[string]any:
		// An object from a var: its keys have no written order, so they go
		// sorted, which is at least the same on every run.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		doc := make(models.Doc, 0, len(t))
		for _, k := range keys {
			doc = append(doc, models.Field{Key: k, Value: t[k]})
		}
		return toBSON(doc)
	case []any:
		a := make(bson.A, 0, len(t))
		for i, e := range t {
			x, err := toBSON(e)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			a = append(a, x)
		}
		return a, nil
	case float64:
		if t == math.Trunc(t) && t >= math.MinInt32 && t <= math.MaxInt32 {
			return int32(t), nil
		}
		return t, nil
	case models.FileArg:
		return nil, errors.New("a file cannot go in a command; send its contents as base64(file(...))")
	}
	return v, nil
}

// extended reads one MongoDB Extended JSON wrapper, and reports whether key
// was one.
func extended(key string, v any) (any, bool, error) {
	s, isString := v.(string)
	switch key {
	case "$oid":
		id, err := bson.ObjectIDFromHex(s)
		if err != nil || !isString {
			return nil, true, fmt.Errorf("$oid %v is not 24 hex digits", v)
		}
		return id, true, nil
	case "$date":
		if ms, ok := v.(float64); ok {
			return bson.DateTime(int64(ms)), true, nil
		}
		at, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, true, fmt.Errorf("$date %v is not an RFC 3339 time or milliseconds", v)
		}
		return bson.NewDateTimeFromTime(at), true, nil
	case "$numberDecimal":
		d, err := bson.ParseDecimal128(s)
		if err != nil {
			return nil, true, fmt.Errorf("$numberDecimal %v is not a decimal", v)
		}
		return d, true, nil
	case "$numberLong":
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, true, fmt.Errorf("$numberLong %v is not a whole number", v)
		}
		return n, true, nil
	}
	return nil, false, nil
}

// fromBSON is a reply's value in the evaluator's domain, by the rules a SQL
// column follows: an ObjectId is its hex, a date an RFC 3339 time, a decimal a
// number when the number reads back as the same decimal.
func fromBSON(v any) any {
	switch t := v.(type) {
	case nil, bson.Null, bson.Undefined:
		return nil
	case bson.D:
		out := make(map[string]any, len(t))
		for _, e := range t {
			out[e.Key] = fromBSON(e.Value)
		}
		return out
	case bson.M:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = fromBSON(e)
		}
		return out
	case bson.A:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = fromBSON(e)
		}
		return out
	case bson.ObjectID:
		return t.Hex()
	case bson.DateTime:
		return t.Time().UTC().Format(time.RFC3339Nano)
	case bson.Decimal128:
		return decimal(t.String())
	case bson.Binary:
		return base64.StdEncoding.EncodeToString(t.Data)
	case bson.Timestamp:
		return map[string]any{"t": float64(t.T), "i": float64(t.I)}
	case bson.Regex:
		return "/" + t.Pattern + "/" + t.Options
	case int32:
		return float64(t)
	case int64:
		return whole(t)
	case float64, string, bool:
		return t
	}
	return fmt.Sprint(v)
}
