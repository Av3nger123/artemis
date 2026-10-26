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
		// A date is a day. Oracle's DATE also holds a time of day, so one
		// that has one is a time like any other.
		if typ == "DATE" && v.Hour() == 0 && v.Minute() == 0 && v.Second() == 0 && v.Nanosecond() == 0 {
			return v.Format("2006-01-02")
		}
		return v.UTC().Format(time.RFC3339Nano)
	case []byte:
		if typ == "UNIQUEIDENTIFIER" && len(v) == 16 {
			return guid(v)
		}
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
	case decimalTypes[typ]:
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

// guid is a SQL Server UNIQUEIDENTIFIER in the form it is written: the server
// stores its first three groups little-endian, so they are reversed.
func guid(b []byte) string {
	order := []int{3, 2, 1, 0, -1, 5, 4, -1, 7, 6, -1, 8, 9, -1, 10, 11, 12, 13, 14, 15}
	var sb strings.Builder
	for _, i := range order {
		if i < 0 {
			sb.WriteByte('-')
			continue
		}
		fmt.Fprintf(&sb, "%02x", b[i])
	}
	return sb.String()
}

var decimalTypes = map[string]bool{
	"NUMERIC": true, "DECIMAL": true, "NEWDECIMAL": true, "NUMBER": true, "MONEY": true, "SMALLMONEY": true,
}

var integerTypes = map[string]bool{
	"INT": true, "INTEGER": true, "INT2": true, "INT4": true, "INT8": true,
	"TINYINT": true, "SMALLINT": true, "MEDIUMINT": true, "BIGINT": true, "YEAR": true,
}

var floatTypes = map[string]bool{
	"FLOAT": true, "FLOAT4": true, "FLOAT8": true, "DOUBLE": true, "REAL": true,
	"BINARY_FLOAT": true, "BINARY_DOUBLE": true,
}

var binaryTypes = map[string]bool{
	"BYTEA": true, "BLOB": true, "TINYBLOB": true, "MEDIUMBLOB": true, "LONGBLOB": true,
	"BINARY": true, "VARBINARY": true, "IMAGE": true, "RAW": true, "LONG RAW": true,
}
