package dbstep

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxExact is the largest whole number a float64 holds exactly. A column value
// past it is a string, so an id of 2^60 reads back as the id it is rather than
// as a rounded number that compares equal to its neighbours.
const maxExact = 1 << 53

// value is one column value in the evaluator's JSON domain: nil, bool,
// float64, string, []any or map[string]any.
//
// The driver's Go type decides first, and the column's database type where
// the Go type says too little: MySQL's text protocol hands every value back as
// bytes, and Postgres's NUMERIC arrives as a string.
func value(dbType string, raw any) any {
	typ := baseType(dbType)
	switch v := raw.(type) {
	case nil:
		return nil
	case bool:
		return v
	case int64:
		return whole(v)
	case int32:
		return float64(v)
	case int:
		return whole(int64(v))
	case uint64:
		if v <= maxExact {
			return float64(v)
		}
		return strconv.FormatUint(v, 10)
	case float64:
		return v
	case float32:
		return float64(v)
	case time.Time:
		if typ == "DATE" {
			return v.Format("2006-01-02")
		}
		return v.UTC().Format(time.RFC3339Nano)
	case []byte:
		if binaryTypes[typ] || !utf8.Valid(v) {
			return base64.StdEncoding.EncodeToString(v)
		}
		return text(typ, string(v))
	case string:
		return text(typ, v)
	}
	return fmt.Sprint(raw)
}

// text reads a value that arrived as text by its column's type.
func text(typ, s string) any {
	switch {
	case typ == "JSON" || typ == "JSONB":
		var v any
		if json.Unmarshal([]byte(s), &v) == nil {
			return v
		}
	case integerTypes[typ]:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return whole(n)
		}
		if _, err := strconv.ParseUint(s, 10, 64); err == nil {
			return s
		}
	case floatTypes[typ]:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case typ == "NUMERIC" || typ == "DECIMAL" || typ == "NEWDECIMAL":
		return decimal(s)
	}
	return s
}

// whole is an integer as a number when a float64 holds it exactly, and as its
// digits when not.
func whole(n int64) any {
	if n <= maxExact && n >= -maxExact {
		return float64(n)
	}
	return strconv.FormatInt(n, 10)
}

// decimal is a NUMERIC as a number when the number reads back as the same
// decimal -- 12.50, 0.10 -- and as its text when not, so 21 digits of money are
// never quietly rounded to 17.
func decimal(s string) any {
	want, ok := new(big.Rat).SetString(s)
	if !ok {
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return s
	}
	got, _ := new(big.Rat).SetString(strconv.FormatFloat(f, 'f', -1, 64))
	if got == nil || got.Cmp(want) != 0 {
		return s
	}
	return f
}

// baseType is a database type name with its size and its sign dropped, so
// "UNSIGNED BIGINT", "DECIMAL(10,2)" and "decimal" read alike.
func baseType(t string) string {
	t = strings.ToUpper(strings.TrimSpace(t))
	t = strings.TrimPrefix(t, "UNSIGNED ")
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

var integerTypes = map[string]bool{
	"INT": true, "INTEGER": true, "INT2": true, "INT4": true, "INT8": true,
	"TINYINT": true, "SMALLINT": true, "MEDIUMINT": true, "BIGINT": true, "YEAR": true,
}

var floatTypes = map[string]bool{
	"FLOAT": true, "FLOAT4": true, "FLOAT8": true, "DOUBLE": true, "REAL": true,
}

var binaryTypes = map[string]bool{
	"BYTEA": true, "BLOB": true, "TINYBLOB": true, "MEDIUMBLOB": true, "LONGBLOB": true,
	"BINARY": true, "VARBINARY": true,
}
