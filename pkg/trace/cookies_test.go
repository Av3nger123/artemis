package trace

import "testing"

// A cookie a response set is a credential nobody declared, so a trace withholds
// its value -- in Set-Cookie and in `cookies` -- and keeps its name and
// attributes, without touching the roots the step's assertions read.
func TestCookieValuesAreWithheld(t *testing.T) {
	type headers map[string]any // the shape eval.Headers has
	roots := map[string]any{
		"headers": headers{"Set-Cookie": "sid=abc; HttpOnly", "Content-Type": "text/plain"},
		"cookies": map[string]any{"sid": map[string]any{"value": "abc", "http_only": true}},
		"status":  float64(200),
	}
	got := withheldCookies(roots)

	h := got["headers"].(map[string]any)
	if h["Set-Cookie"] != Redacted || h["Content-Type"] != "text/plain" {
		t.Errorf("headers = %v", h)
	}
	sid := got["cookies"].(map[string]any)["sid"].(map[string]any)
	if sid["value"] != Redacted || sid["http_only"] != true {
		t.Errorf("cookies.sid = %v", sid)
	}
	if roots["cookies"].(map[string]any)["sid"].(map[string]any)["value"] != "abc" {
		t.Error("withholding reached back into the roots the assertions read")
	}
	if got["status"] != float64(200) {
		t.Errorf("status = %v", got["status"])
	}
}
