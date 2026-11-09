package token

import (
	"strings"
	"testing"
)

func TestSnakeFrom(t *testing.T) {
	for in, want := range map[string]string{
		"Get a token":       "get_a_token",
		"SignIn":            "sign_in",
		"log in":            "log_in",
		"  KEK rotation!! ": "kek_rotation",
		"2fa works":         "s_2fa_works",
		"!!!":               "unnamed",
		"already_snake":     "already_snake",
		"HTTPServer up":     "httpserver_up",
		"true":              "s_true",
		"Null":              "s_null",
	} {
		if got := SnakeFrom(in); got != want {
			t.Errorf("SnakeFrom(%q) = %q, want %q", in, got, want)
		}
	}
	long := "the quick brown fox jumps over the lazy dog and keeps on running"
	got := SnakeFrom(long)
	if len(got) > 40 || strings.HasSuffix(got, "_") || !strings.HasPrefix(long, strings.ReplaceAll(got, "_", " ")) {
		t.Errorf("SnakeFrom(long) = %q, want at most 40 characters ending on a whole word", got)
	}
}

func TestIsSnakeID(t *testing.T) {
	for _, s := range []string{"a", "sign_in", "v2_api", "kek2"} {
		if !IsSnakeID(s) {
			t.Errorf("IsSnakeID(%q) = false", s)
		}
	}
	for _, s := range []string{"", "Sign_in", "sign__in", "_x", "x_", "2x", "sign-in", "signIn", "true", "false", "null"} {
		if IsSnakeID(s) {
			t.Errorf("IsSnakeID(%q) = true", s)
		}
	}
	for _, s := range []string{"Get a token", "SignIn", "2fa works", "!!!"} {
		if got := SnakeFrom(s); !IsSnakeID(got) {
			t.Errorf("SnakeFrom(%q) = %q, which is not an id", s, got)
		}
	}
}
