package eval

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fixedNow is the clock the value builtins' tests read: a moment with a
// fractional second, so now() is seen to drop it.
var fixedNow = time.Date(2026, 10, 26, 9, 30, 15, 500_000_000, time.UTC)

func genEnv() *Env {
	return &Env{
		Vars: map[string]any{
			"items": []any{"a", "b", "c"},
			"obj":   map[string]any{"b": float64(1), "a": "<x>"},
		},
		Gen: NewGen(42, "suite/a.art", "checkout").WithClock(func() time.Time { return fixedNow }),
	}
}

// Every value builtin, with the value it makes from a fixed input. The hashes
// are the published test vectors, so a wrong algorithm cannot pass by
// agreeing with itself.
func TestValueBuiltins(t *testing.T) {
	jwt := "eyJhbGciOiJub25lIn0." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"1","role":"admin"}`)) + ".sig"
	cases := []struct {
		src  string
		want any
	}{
		{`now()`, "2026-10-26T09:30:15Z"},
		{`now("rfc3339")`, "2026-10-26T09:30:15Z"},
		{`now("date")`, "2026-10-26"},
		{`now("unix")`, float64(fixedNow.Unix())},
		{`now("unix_ms")`, float64(fixedNow.UnixMilli())},
		{`base64("user:password")`, "dXNlcjpwYXNzd29yZA=="},
		{`base64_decode("dXNlcjpwYXNzd29yZA==")`, "user:password"},
		{`base64_decode("dXNlcjpwYXNzd29yZA")`, "user:password"},
		{`base64_decode("-_8")`, "\xfb\xff"},
		{`url_encode("a b&c/d")`, "a+b%26c%2Fd"},
		{`sha256("abc")`, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{`hmac_sha256("key", "The quick brown fox jumps over the lazy dog")`,
			"f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"},
		{`jwt_claims("` + jwt + `").role`, "admin"},
		{`to_json(obj)`, `{"a":"<x>","b":1}`},
		{`to_json([1, "two", null, true])`, `[1,"two",null,true]`},
		{`to_json("x")`, `"x"`},
		{`from_json("{\"n\": 2}").n`, float64(2)},
		{`len(items)`, float64(3)},
		{`len(obj)`, float64(2)},
		{`len("héllo")`, float64(5)},
		{`number("42")`, float64(42)},
		{`number(" 1.5 ")`, 1.5},
		{`number(7)`, float64(7)},
		{`string(200)`, "200"},
		{`string({"b": 1, "a": [true]})`, `{"a":[true],"b":1}`},
		{`string(obj) == "${obj}"`, true},
		{`string(null)`, "null"},
	}
	for _, c := range cases {
		got, err := evalSrc(t, c.src, genEnv())
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if !equal(got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.src, got, c.want)
		}
	}
}

// What each value builtin says when it cannot answer: an errored assertion
// naming the reason, never a value that hides the mistake.
func TestValueBuiltinErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{`now("iso")`, `now()'s argument must be one of rfc3339, date, unix, unix_ms, got "iso"`},
		{`random_int(10, 1)`, "random_int()'s bounds are the wrong way round: 10 is above 1"},
		{`random_int(1.5, 3)`, "random_int()'s argument must be a whole number, got 1.5"},
		{`random_int("a", 3)`, "random_int()'s argument must be a number, got string"},
		{`random_string(0)`, "random_string() makes 1 to 256 characters, and this call asks for 0"},
		{`random_string(257)`, "random_string() makes 1 to 256 characters, and this call asks for 257"},
		{`base64_decode("%%%")`, "base64_decode()'s argument is not base64"},
		{`base64(1)`, "base64()'s argument must be a string or a file, got number"},
		{`jwt_claims("a.b")`, "jwt_claims()'s argument is not a JWT: it has 2 part(s)"},
		{`from_json("{")`, "from_json()'s argument is not JSON"},
		{`len(1)`, "len() measures a string, an array or an object, and 1 is a number"},
		{`number("abc")`, `number() cannot read "abc" as a number: it is abc`},
		{`number(true)`, "number() cannot read true as a number"},
		{`to_json(/x/)`, "to_json() cannot write a regular expression"},
	}
	for _, c := range cases {
		_, err := evalSrc(t, c.src, genEnv())
		if err == nil {
			t.Errorf("%s: no error, want %q", c.src, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s:\n got: %v\nwant: %s", c.src, err, c.want)
		}
	}
}

// A scenario's generator is a function of the run's seed, its file and its
// name -- not of when it ran -- so the same three give the same values and any
// other scenario gives different ones.
func TestTheGeneratorIsSeededByScenario(t *testing.T) {
	draw := func(seed uint64, file, scenario string) string {
		env := &Env{Gen: NewGen(seed, file, scenario)}
		var out []string
		for _, src := range []string{`uuid()`, `random_int(1, 1000000)`, `random_string(16)`} {
			v, err := evalSrc(t, src, env)
			if err != nil {
				t.Fatalf("%s: %v", src, err)
			}
			out = append(out, Render(v))
		}
		return strings.Join(out, " ")
	}
	first := draw(42, "a.art", "checkout")
	if again := draw(42, "a.art", "checkout"); again != first {
		t.Errorf("the same seed, file and scenario drew %q and then %q", first, again)
	}
	for _, other := range []string{draw(43, "a.art", "checkout"), draw(42, "b.art", "checkout"), draw(42, "a.art", "refund")} {
		if other == first {
			t.Errorf("a different seed, file or scenario drew the same values: %q", first)
		}
	}
}

// What the random builtins make is the shape SPEC.md promises.
func TestRandomValuesHaveTheirShape(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	word := regexp.MustCompile(`^[a-z0-9]{12}$`)
	env := genEnv()
	for i := 0; i < 200; i++ {
		u, _ := evalSrc(t, `uuid()`, env)
		if !uuid.MatchString(u.(string)) {
			t.Fatalf("uuid() = %q, not a version 4 UUID", u)
		}
		s, _ := evalSrc(t, `random_string(12)`, env)
		if !word.MatchString(s.(string)) {
			t.Fatalf("random_string(12) = %q", s)
		}
		n, _ := evalSrc(t, `random_int(-2, 2)`, env)
		if f := n.(float64); f < -2 || f > 2 || f != float64(int(f)) {
			t.Fatalf("random_int(-2, 2) = %v", f)
		}
	}
	one, _ := evalSrc(t, `random_int(5, 5)`, env)
	if one != float64(5) {
		t.Errorf("random_int(5, 5) = %v, want 5", one)
	}
}

// Used is what decides whether a run prints its seed: only a draw from the
// generator sets it, and the clock is not a draw.
func TestUsedIsSetByADrawOnly(t *testing.T) {
	env := genEnv()
	if _, err := evalSrc(t, `now()`, env); err != nil {
		t.Fatal(err)
	}
	if env.Gen.Used() {
		t.Error("now() marked the generator used; a seed cannot reproduce a clock")
	}
	if _, err := evalSrc(t, `uuid()`, env); err != nil {
		t.Fatal(err)
	}
	if !env.Gen.Used() {
		t.Error("uuid() did not mark the generator used")
	}
	var none *Gen
	if none.Used() {
		t.Error("a nil Gen reports itself used")
	}
}

// An expression evaluated outside a run has no Gen, and the value builtins
// still answer: from the process's own generator and clock.
func TestValueBuiltinsWithoutAGen(t *testing.T) {
	for _, src := range []string{`uuid()`, `now()`, `random_int(1, 3)`, `random_string(4)`} {
		if _, err := evalSrc(t, src, nil); err != nil {
			t.Errorf("%s with no Env: %v", src, err)
		}
	}
}
