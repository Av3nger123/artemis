package eval

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/builtin"
)

// Gen is what the builtins that make a value up draw on: a scenario's random
// generator and its clock.
//
// One per scenario, seeded from the run's seed and the scenario's file and
// name rather than from the order scenarios happen to run in, so `--jobs 8`
// makes the values `--jobs 1` does and `--seed` reproduces a failure. A
// scenario's steps run one after another, so a Gen is never shared between
// goroutines.
type Gen struct {
	rand *rand.Rand
	now  func() time.Time
	used bool
}

// NewGen is the generator for one scenario of a run seeded with seed.
func NewGen(seed uint64, file, scenario string) *Gen {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", seed, file, scenario)))
	return &Gen{
		rand: rand.New(rand.NewPCG(binary.LittleEndian.Uint64(sum[:8]), binary.LittleEndian.Uint64(sum[8:16]))),
		now:  time.Now,
	}
}

// WithClock is g with its clock replaced, for a test that needs now() to
// answer something it can assert.
func (g *Gen) WithClock(now func() time.Time) *Gen {
	g.now = now
	return g
}

// Used reports whether anything drew from the generator. A run's seed is worth
// printing only when a value came out of it.
func (g *Gen) Used() bool { return g != nil && g.used }

// int64n is a uniform integer in [0, n). A nil Gen -- an expression evaluated
// outside a run, by a test or a UI -- draws from the process's own generator,
// which is random and is fine there: nothing is reproducing that run.
func (g *Gen) int64n(n int64) int64 {
	if g == nil || g.rand == nil {
		return rand.Int64N(n)
	}
	g.used = true
	return g.rand.Int64N(n)
}

func (g *Gen) uint64() uint64 {
	if g == nil || g.rand == nil {
		return rand.Uint64()
	}
	g.used = true
	return g.rand.Uint64()
}

func (g *Gen) clock() time.Time {
	if g == nil || g.now == nil {
		return time.Now()
	}
	return g.now()
}

// argument converts one evaluated argument to what its parameter takes, or
// says why it cannot. The wording for a string is the wording evalCall used
// before builtins took anything else, which the goldens hold.
func argument(name string, p builtin.Param, x ast.Expr, v any) (any, error) {
	switch p.Type {
	case builtin.TypeString:
		s, ok := v.(string)
		if !ok {
			return nil, errorf(x.Span(), "%s()'s argument must be a string, got %s at %s", name, typeOf(v), exprText(x))
		}
		if len(p.OneOf) > 0 && !oneOf(s, p.OneOf) {
			return nil, errorf(x.Span(), "%s()'s argument must be one of %s, got %q",
				name, strings.Join(p.OneOf, ", "), s)
		}
		return s, nil
	case builtin.TypeNumber:
		f, ok := number(v)
		if !ok {
			return nil, errorf(x.Span(), "%s()'s argument must be a number, got %s at %s", name, typeOf(v), exprText(x))
		}
		if p.Whole && (f != math.Trunc(f) || math.Abs(f) > maxExact) {
			return nil, errorf(x.Span(), "%s()'s argument must be a whole number, got %s at %s", name, Render(f), exprText(x))
		}
		return f, nil
	}
	return v, nil
}

// maxExact is the largest whole number a float64 holds exactly. A bound past it
// would name a number the generator cannot produce.
const maxExact = 1 << 53

// maxRandomString is the longest random_string(): long enough for any test
// value, short enough that a typo of an extra digit is an error and not a
// megabyte in a URL.
const maxRandomString = 256

const randomAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func oneOf(s string, set []string) bool {
	for _, w := range set {
		if s == w {
			return true
		}
	}
	return false
}

// evalValueFn evaluates the builtins that read no page and no environment:
// the ones that make a value up, and the ones that turn one value into another.
func evalValueFn(c *ast.Call, name string, args []any, env *Env) (any, error) {
	gen := env.gen()
	switch name {
	case "uuid":
		var b [16]byte
		binary.LittleEndian.PutUint64(b[:8], gen.uint64())
		binary.LittleEndian.PutUint64(b[8:], gen.uint64())
		b[6] = b[6]&0x0f | 0x40 // version 4
		b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil

	case "now":
		format := "rfc3339"
		if len(args) == 1 {
			format = args[0].(string)
		}
		t := gen.clock().UTC()
		switch format {
		case "date":
			return t.Format("2006-01-02"), nil
		case "unix":
			return float64(t.Unix()), nil
		case "unix_ms":
			return float64(t.UnixMilli()), nil
		}
		return t.Format(time.RFC3339), nil

	case "random_int":
		low, high := args[0].(float64), args[1].(float64)
		if low > high {
			return nil, errorf(c.Span(), "random_int()'s bounds are the wrong way round: %s is above %s",
				Render(low), Render(high))
		}
		return low + float64(gen.int64n(int64(high-low)+1)), nil

	case "random_string":
		n := args[0].(float64)
		if n < 1 || n > maxRandomString {
			return nil, errorf(c.Span(), "random_string() makes 1 to %d characters, and this call asks for %s",
				maxRandomString, Render(n))
		}
		out := make([]byte, int(n))
		for i := range out {
			out[i] = randomAlphabet[gen.int64n(int64(len(randomAlphabet)))]
		}
		return string(out), nil

	case "base64":
		return base64.StdEncoding.EncodeToString([]byte(args[0].(string))), nil

	case "base64_decode":
		b, err := decodeBase64(args[0].(string))
		if err != nil {
			return nil, errorf(c.Span(), "base64_decode()'s argument is not base64: %v", err)
		}
		return string(b), nil

	case "url_encode":
		return url.QueryEscape(args[0].(string)), nil

	case "sha256":
		sum := sha256.Sum256([]byte(args[0].(string)))
		return hex.EncodeToString(sum[:]), nil

	case "hmac_sha256":
		mac := hmac.New(sha256.New, []byte(args[0].(string)))
		mac.Write([]byte(args[1].(string)))
		return hex.EncodeToString(mac.Sum(nil)), nil

	case "jwt_claims":
		return jwtClaims(c, args[0].(string))

	case "to_json":
		return toJSON(c, args[0])

	case "from_json":
		var v any
		if err := json.Unmarshal([]byte(args[0].(string)), &v); err != nil {
			return nil, errorf(c.Span(), "from_json()'s argument is not JSON: %v", err)
		}
		return v, nil

	case "len":
		switch t := args[0].(type) {
		case string:
			return float64(utf8.RuneCountInString(t)), nil
		case []any:
			return float64(len(t)), nil
		case map[string]any:
			return float64(len(t)), nil
		case Headers:
			return float64(len(t)), nil
		}
		return nil, errorf(c.Span(), "len() measures a string, an array or an object, and %s is %s",
			exprText(c.Args[0].Value), article(typeOf(args[0])))

	case "number":
		if _, isBool := args[0].(bool); !isBool {
			if f, ok := number(args[0]); ok {
				return f, nil
			}
		}
		return nil, errorf(c.Span(), "number() cannot read %s as a number: it is %s",
			exprText(c.Args[0].Value), Render(args[0]))

	case "string":
		return Render(args[0]), nil
	}
	return nil, errorf(c.Span(), "%s() has no evaluation", name)
}

// decodeBase64 reads standard or URL-safe base64, padded or not: the four
// spellings an API hands back, none of which a test author should have to name.
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	var firstErr error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		b, err := enc.DecodeString(s)
		if err == nil {
			return b, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// jwtClaims is a token's payload, decoded and **not** verified: a test asserts
// what a token says, and checking that it was signed by the right key is the
// service's job, not the test's.
func jwtClaims(c *ast.Call, token string) (any, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, errorf(c.Span(), "jwt_claims()'s argument is not a JWT: it has %d part(s), and a JWT has 3", len(parts))
	}
	b, err := decodeBase64(parts[1])
	if err != nil {
		return nil, errorf(c.Span(), "jwt_claims(): the token's payload is not base64: %v", err)
	}
	var claims any
	if err := json.Unmarshal(b, &claims); err != nil {
		return nil, errorf(c.Span(), "jwt_claims(): the token's payload is not JSON: %v", err)
	}
	if _, ok := claims.(map[string]any); !ok {
		return nil, errorf(c.Span(), "jwt_claims(): the token's payload is %s, not an object", article(typeOf(claims)))
	}
	return claims, nil
}

// toJSON is v as compact JSON with its keys in order, and with <, > and &
// written as themselves: the text a server would have sent, not one escaped
// for an HTML page.
func toJSON(c *ast.Call, v any) (any, error) {
	if _, ok := v.(Regexp); ok {
		return nil, errorf(c.Span(), "to_json() cannot write a regular expression")
	}
	if h, ok := v.(Headers); ok {
		v = map[string]any(h)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, errorf(c.Span(), "to_json(): %v", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
