package codegen

// The Python the generated file carries with it.
//
// Every helper here exists for one reason: the DSL has an operator Python does
// not, or has one whose meaning differs, and the difference is pkg/eval's
// documented behaviour rather than an implementation detail. Each is written
// against the paragraph of pkg/eval that defines it, and the doc comment says
// which, so a reader of the generated file can tell what it is faithful to.
//
// They are emitted on demand. A file of api scenarios carries no settle loop and
// no Playwright fixture; a file that never writes `contains` carries no
// art_contains. The order is the order of the table, which is fixed, so two runs
// over the same file produce the same bytes.

// The helper keys, which are also the Python names.
const (
	helperRender   = "art_render"
	helperEnv      = "art_env"
	helperSeconds  = "art_seconds"
	helperJSON     = "art_json"
	helperExists   = "art_exists"
	helperIs       = "art_is"
	helperContains = "art_contains"
	helperMatches  = "art_matches"
	helperMatch    = "art_match"

	// The value builtins: one helper each, named for the builtin, so a reader
	// of a generated file can find the rule in SPEC.md by the name.
	helperStr          = "art_str"
	helperUUID         = "art_uuid"
	helperNow          = "art_now"
	helperWhole        = "art_whole"
	helperRandomInt    = "art_random_int"
	helperRandomString = "art_random_string"
	helperBase64       = "art_base64"
	helperBase64Decode = "art_base64_decode"
	helperURLEncode    = "art_url_encode"
	helperSHA256       = "art_sha256"
	helperHMACSHA256   = "art_hmac_sha256"
	helperJWTClaims    = "art_jwt_claims"
	helperToJSON       = "art_to_json"
	helperFromJSON     = "art_from_json"
	helperLen          = "art_len"
	helperNumber       = "art_number"
	helperString       = "art_string"
	helperCookies      = "art_cookies"
	helperText         = "art_text"
	helperValue        = "art_value"
	helperAttr         = "art_attr"
	helperArgv         = "art_argv"
	helperGoto         = "art_goto"
	helperRetry        = "art_retry"
	helperWithin       = "art_within"
	helperBrowser      = "browser"
)

// helper is one emitted definition: its Python source, the modules it imports
// and the other helpers it calls.
type helper struct {
	name    string
	imports []string
	needs   []string
	src     string
}

// helpers are in emission order. Nothing here is alphabetical: art_render comes
// first because most of the others call it, and the fixture comes last because
// it is the only one that is not a function.
var helpers = []helper{{
	name:    helperRender,
	imports: []string{"json"},
	src: `def art_render(value):
    """Render a value the way artemis does when it goes into a string: a whole
    number without a trailing .0, a boolean as true or false, null as "null",
    and a list or an object as compact JSON. This is eval.Render, which is what
    makes a URL built here and a URL built by ` + "`artemis run`" + ` the same string."""
    if value is None:
        return "null"
    if isinstance(value, str):
        return value
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    if isinstance(value, (int, float)):
        return repr(value)
    return json.dumps(value, separators=(",", ":"))
`,
}, {
	name:    helperEnv,
	imports: []string{"os"},
	src: `def art_env(name, default=None):
    """Read an environment variable the way artemis does. A variable that is
    not set, that is empty, or that holds only space characters has no value:
    with no default this raises, rather than giving the empty string that used
    to go straight into a URL. This is eval.Env.getenv, and the two must agree
    -- a scenario that fails under ` + "`artemis run`" + ` has to fail here too."""
    value = os.environ.get(name)
    if value is not None and value.strip() != "":
        return value
    if default is not None:
        if not isinstance(default, str):
            # The checker only judges a *literal* default, so env("PORT", port)
            # reaches here with whatever port holds. The interpreter errors on
            # it; returning it would put a number -- or a null that renders as
            # "null" -- into a URL, which is the fault this helper exists to
            # stop. eval's words, not Python's, so the two targets read alike.
            kinds = {bool: "boolean", int: "number", float: "number",
                     list: "array", dict: "object"}
            raise TypeError("env()'s argument must be a string, got %s"
                            % kinds.get(type(default), type(default).__name__))
        return default
    raise AssertionError("the environment variable %s has no value" % name)
`,
}, {
	name:    helperSeconds,
	imports: []string{"re"},
	src: `def art_seconds(text):
    """A duration the way the DSL writes it -- "500ms", "5s", "1m30s" -- as a
    number of seconds. A ` + "`timeout`" + `, a retry ` + "`delay`" + ` or a ` + "`within`" + ` budget may come
    out of an expression, so the parsing has to happen at run time too; a literal
    one is converted when the file is generated and never reaches this."""
    found = re.findall(r"([0-9]*\.?[0-9]+)(ns|us|ms|s|m|h)", text)
    if not found:
        raise ValueError(
            "%r is not a duration (want something like \"500ms\" or \"5s\")" % (text,))
    units = {"ns": 1e-9, "us": 1e-6, "ms": 1e-3, "s": 1.0, "m": 60.0, "h": 3600.0}
    return sum(float(n) * units[u] for n, u in found)
`,
}, {
	name: helperJSON,
	src: `def art_json(response):
    """What an api step binds to ` + "`body`" + `: the response decoded as any JSON
    value, or None when it is not JSON. A body that is not JSON is not a
    failure -- whether the step needed one is what its asserts say."""
    try:
        return response.json()
    except ValueError:
        return None
`,
}, {
	name: helperExists,
	src: `def art_exists(read):
    """` + "`expr exists`" + `: false for a path that does not resolve and false for a
    JSON null, which is eval's rule -- a null is treated as absent, and ` + "`exists`" + `
    is the operator for asking rather than an error.

    The read is a lambda because an absent path is a KeyError: evaluating it
    before the call would raise where this has to answer False. Reading a field
    of a number raises TypeError, which artemis reports as an error and this
    answers False to -- the one narrowing in this file, and the price of using
    Python's own subscript so the generated line reads as a path."""
    try:
        return read() is not None
    except (KeyError, IndexError, TypeError):
        return False
`,
}, {
	name: helperIs,
	src: `def art_type(value):
    """A value's JSON type, in the names ` + "`is`" + ` accepts."""
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, str):
        return "string"
    if isinstance(value, (int, float)):
        return "number"
    if isinstance(value, (list, tuple)):
        return "array"
    if isinstance(value, dict) or hasattr(value, "keys"):
        return "object"
    return "unknown"


def art_is(value, want):
    """` + "`expr is <type>`" + `, over the six JSON type names."""
    return art_type(value) == want
`,
}, {
	name:  helperContains,
	needs: []string{helperRender},
	src: `def art_contains(haystack, needle):
    """` + "`contains`" + `: a substring of a string, an element of an array, or a key of
    an object. The three readings come from the left operand's type, which is
    eval's rule; a left operand of any other type is a mistake with no honest
    false, so it raises."""
    if isinstance(haystack, str):
        if needle is None or isinstance(needle, (list, tuple, dict)):
            raise TypeError("contains against a string needs a string")
        return art_render(needle) in haystack
    if isinstance(haystack, (list, tuple)):
        return needle in haystack
    if isinstance(haystack, dict) or hasattr(haystack, "keys"):
        return needle in haystack
    raise TypeError("contains needs a string, array or object on the left")
`,
}, {
	name:    helperMatches,
	imports: []string{"re"},
	needs:   []string{helperRender},
	src: `def art_matches(subject, pattern):
    """` + "`matches`" + `: an unanchored search, as a regex usually is. A composite or a
    null has no rendering worth matching and raises rather than matching
    "null"."""
    if subject is None or isinstance(subject, (list, tuple, dict)):
        raise TypeError("matches needs a string on the left")
    return re.search(pattern, art_render(subject)) is not None
`,
}, {
	name:    helperMatch,
	imports: []string{"re"},
	needs:   []string{helperRender},
	src: `def art_match(subject, pattern):
    """` + "`match(text, /re/)`" + `: capturing group 1 when the pattern has one and the
    whole match when it has none. Two groups is a mistake rather than a choice,
    and a pattern that matched nothing is an error rather than an empty string --
    the text was there and the question was asked. The value is always a string,
    because reading "007" as seven loses data."""
    compiled = re.compile(pattern)
    if compiled.groups > 1:
        raise ValueError(
            "pattern %r has %d capturing groups; match() takes one or none"
            % (pattern, compiled.groups))
    found = compiled.search(art_render(subject))
    if found is None:
        raise AssertionError("pattern %r matched nothing" % (pattern,))
    return found.group(1) if compiled.groups == 1 else found.group(0)
`,
}, {
	name: helperStr,
	src: `def art_str(name, value):
    """The text a builtin's argument has to be. eval converts each argument to
    what its parameter takes, and a value that is not text is a mistake in the
    expression rather than something to coerce."""
    if not isinstance(value, str):
        raise TypeError("%s()'s argument must be a string, got %r" % (name, value))
    return value
`,
}, {
	name:    helperUUID,
	imports: []string{"uuid"},
	src: `def art_uuid():
    """` + "`uuid()`" + `: a random version 4 UUID. The interpreter draws it from a
    seeded generator so --seed repeats a failed run's values; a generated test is
    not repeating an interpreter run, so it draws from Python's own."""
    return str(uuid.uuid4())
`,
}, {
	name:    helperNow,
	imports: []string{"datetime"},
	src: `def art_now(format="rfc3339"):
    """` + "`now()`" + `: the time in UTC, as a timestamp with no fraction, a date, or
    seconds or milliseconds since the epoch -- eval's four formats and no other."""
    moment = datetime.datetime.now(datetime.timezone.utc)
    if format == "rfc3339":
        return moment.strftime("%Y-%m-%dT%H:%M:%SZ")
    if format == "date":
        return moment.strftime("%Y-%m-%d")
    if format == "unix":
        return int(moment.timestamp())
    if format == "unix_ms":
        return int(moment.timestamp() * 1000)
    raise ValueError(
        "now()'s argument must be one of rfc3339, date, unix, unix_ms, got %r" % (format,))
`,
}, {
	name:  helperWhole,
	needs: []string{helperNumber},
	src: `def art_whole(name, value):
    """A count or a bound, read the way eval reads one: a number, or text that
    reads as one, with no fractional part and small enough to be exact."""
    number = art_number(value)
    if number != int(number) or abs(number) > 2 ** 53:
        raise ValueError("%s()'s argument must be a whole number, got %r" % (name, value))
    return int(number)
`,
}, {
	name:    helperRandomInt,
	imports: []string{"random"},
	needs:   []string{helperWhole},
	src: `def art_random_int(low, high):
    """` + "`random_int(min, max)`" + `: a whole number from min to max, both included."""
    low, high = art_whole("random_int", low), art_whole("random_int", high)
    if low > high:
        raise ValueError(
            "random_int()'s bounds are the wrong way round: %d is above %d" % (low, high))
    return random.randint(low, high)
`,
}, {
	name:    helperRandomString,
	imports: []string{"random"},
	needs:   []string{helperWhole},
	src: `def art_random_string(length):
    """` + "`random_string(n)`" + `: n characters of a-z and 0-9, n from 1 to 256."""
    length = art_whole("random_string", length)
    if length < 1 or length > 256:
        raise ValueError(
            "random_string() makes 1 to 256 characters, and this call asks for %d" % (length,))
    return "".join(random.choice("abcdefghijklmnopqrstuvwxyz0123456789") for _ in range(length))
`,
}, {
	name:    helperBase64,
	imports: []string{"base64"},
	needs:   []string{helperStr},
	src: `def art_base64(text):
    """` + "`base64(text)`" + `: standard base64, padded."""
    return base64.b64encode(art_str("base64", text).encode("utf-8")).decode("ascii")
`,
}, {
	name:    helperBase64Decode,
	imports: []string{"base64"},
	needs:   []string{helperStr},
	src: `def art_base64_decode(text):
    """` + "`base64_decode(text)`" + `: standard or URL-safe base64, padded or not --
    the four spellings an API hands back, none of which a test should have to
    name."""
    text = art_str("base64_decode", text).strip().replace("-", "+").replace("_", "/")
    text += "=" * (-len(text) % 4)
    return base64.b64decode(text, validate=True).decode("utf-8", "replace")
`,
}, {
	name:    helperURLEncode,
	imports: []string{"urllib.parse"},
	needs:   []string{helperStr},
	src: `def art_url_encode(text):
    """` + "`url_encode(text)`" + `: query escaping, a space as +, which is Go's
    url.QueryEscape and what the interpreter sends."""
    return urllib.parse.quote_plus(art_str("url_encode", text))
`,
}, {
	name:    helperSHA256,
	imports: []string{"hashlib"},
	needs:   []string{helperStr},
	src: `def art_sha256(text):
    """` + "`sha256(text)`" + `: the digest of the text's UTF-8 bytes, in lower-case hex."""
    return hashlib.sha256(art_str("sha256", text).encode("utf-8")).hexdigest()
`,
}, {
	name:    helperHMACSHA256,
	imports: []string{"hashlib", "hmac"},
	needs:   []string{helperStr},
	src: `def art_hmac_sha256(key, text):
    """` + "`hmac_sha256(key, text)`" + `: the HMAC-SHA256 of the text under the key, in
    lower-case hex."""
    return hmac.new(art_str("hmac_sha256", key).encode("utf-8"),
                    art_str("hmac_sha256", text).encode("utf-8"), hashlib.sha256).hexdigest()
`,
}, {
	name:    helperJWTClaims,
	imports: []string{"json"},
	needs:   []string{helperStr, helperBase64Decode},
	src: `def art_jwt_claims(token):
    """` + "`jwt_claims(token)`" + `: the token's payload, decoded and NOT verified. A test
    asserts what a token says; whether the right key signed it is the service's
    business."""
    parts = art_str("jwt_claims", token).strip().split(".")
    if len(parts) != 3:
        raise ValueError(
            "jwt_claims()'s argument is not a JWT: it has %d part(s), and a JWT has 3" % len(parts))
    claims = json.loads(art_base64_decode(parts[1]))
    if not isinstance(claims, dict):
        raise ValueError("jwt_claims(): the token's payload is not an object")
    return claims
`,
}, {
	name:    helperToJSON,
	imports: []string{"json"},
	src: `def art_to_json(value):
    """` + "`to_json(value)`" + `: compact JSON with its keys in order, a whole number
    without a trailing .0 -- the text the interpreter makes of the same value."""
    def plain(item):
        if item is None or isinstance(item, (bool, str)):
            return item
        if isinstance(item, float) and item.is_integer():
            return int(item)
        if isinstance(item, (int, float)):
            return item
        if isinstance(item, dict):
            return {key: plain(inner) for key, inner in item.items()}
        if isinstance(item, (list, tuple)):
            return [plain(inner) for inner in item]
        raise TypeError("to_json() cannot write %r" % (item,))
    return json.dumps(plain(value), separators=(",", ":"), sort_keys=True, ensure_ascii=False)
`,
}, {
	name:    helperFromJSON,
	imports: []string{"json"},
	needs:   []string{helperStr},
	src: `def art_from_json(text):
    """` + "`from_json(text)`" + `: the value JSON text holds."""
    return json.loads(art_str("from_json", text))
`,
}, {
	name: helperLen,
	src: `def art_len(value):
    """` + "`len(value)`" + `: the characters in a string, the items in an array, the
    keys of an object -- and an error for anything else, not a zero."""
    if isinstance(value, (str, list, dict)):
        return len(value)
    raise TypeError("len() measures a string, an array or an object, not %r" % (value,))
`,
}, {
	name:    helperNumber,
	imports: []string{"math"},
	src: `def art_number(value):
    """` + "`number(value)`" + `: a number, or text that reads as one. A boolean is not a
    number, and neither is text that reads as infinity or NaN."""
    if isinstance(value, bool) or value is None:
        raise ValueError("number() cannot read %r as a number" % (value,))
    if isinstance(value, (int, float)):
        result = float(value)
    elif isinstance(value, str):
        try:
            result = float(value.strip())
        except ValueError:
            raise ValueError("number() cannot read %r as a number" % (value,)) from None
    else:
        raise ValueError("number() cannot read %r as a number" % (value,))
    if math.isnan(result) or math.isinf(result):
        raise ValueError("number() cannot read %r as a number" % (value,))
    return result
`,
}, {
	name:  helperString,
	needs: []string{helperRender},
	src: `def art_string(value):
    """` + "`string(value)`" + `: the text the value has in an interpolation."""
    return art_render(value)
`,
}, {
	name:    helperCookies,
	imports: []string{"datetime", "email.utils"},
	src: `def art_cookies(response):
    """` + "`cookies`" + `: what this response's Set-Cookie headers set, each name to its
    value and attributes, an attribute the server did not send as None. Read off
    the headers as the interpreter reads them rather than off requests' jar,
    which fills in a domain and a path the server never sent."""
    out = {}
    for line in response.raw.headers.getlist("Set-Cookie"):
        parts = [part.strip() for part in line.split(";")]
        name, sep, value = parts[0].partition("=")
        if not sep or not name.strip():
            continue
        value = value.strip()
        if len(value) > 1 and value[0] == value[-1] == '"':
            value = value[1:-1]
        cookie = {"value": value, "domain": None, "path": None, "expires": None,
                  "secure": False, "http_only": False, "same_site": None}
        for attribute in parts[1:]:
            key, _, setting = attribute.partition("=")
            key, setting = key.strip().lower(), setting.strip()
            if key == "domain" and setting:
                cookie["domain"] = setting
            elif key == "path" and setting:
                cookie["path"] = setting
            elif key == "expires" and setting:
                try:
                    moment = email.utils.parsedate_to_datetime(setting)
                    cookie["expires"] = moment.astimezone(
                        datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
                except (TypeError, ValueError):
                    pass
            elif key == "secure":
                cookie["secure"] = True
            elif key == "httponly":
                cookie["http_only"] = True
            elif key == "samesite" and setting.lower() in ("lax", "strict", "none"):
                cookie["same_site"] = setting.lower()
        out[name.strip()] = cookie
    return out
`,
}, {
	name: helperText,
	src: `def art_text(page, selector):
    """` + "`text(sel)`" + `: the first match's text content, or None for no match.

    query_selector and then the handle, not page.text_content(sel): the second
    auto-waits and then errors, which would make ` + "`expect text(\".x\") is null`" + ` a
    thirty-second assertion and a ` + "`within`" + ` budget a fiction."""
    found = page.query_selector(selector)
    return None if found is None else found.text_content()
`,
}, {
	name: helperValue,
	src: `def art_value(page, selector):
    """` + "`value(sel)`" + `: the first match's value as a form control, or None for no
    match."""
    found = page.query_selector(selector)
    return None if found is None else found.input_value()
`,
}, {
	name: helperAttr,
	src: `def art_attr(page, selector, name):
    """` + "`attr(sel, name)`" + `: the named attribute, None for an absent attribute and
    None for no element -- the same answer by design, because ` + "`exists`" + ` is the
    operator for telling them apart.

    getAttribute through evaluate rather than the handle's get_attribute, which
    is the call that can tell an absent attribute from ` + "`href=\"\"`" + `."""
    found = page.query_selector(selector)
    if found is None:
        return None
    return found.evaluate("(el, name) => el.getAttribute(name)", name)
`,
}, {
	name:  helperArgv,
	needs: []string{helperRender},
	src: `def art_argv(command, args):
    """The argv a terminal step runs: the command, trimmed, then every argument
    rendered. There is no word splitting anywhere, which is the whole reason
    ` + "`args`" + ` is a list rather than a string."""
    if args is None:
        args = []
    if not isinstance(args, (list, tuple)):
        raise TypeError('args is not an array like ["-f", "seed.sql"]')
    return [art_render(command).strip()] + [art_render(a) for a in args]
`,
}, {
	name:    helperGoto,
	imports: []string{"urllib"},
	src: `def art_goto(page, target):
    """` + "`goto`" + `: a relative target resolves against where the page already is.

    That is artemis's rule and not playwright's -- a context with no base URL
    answers a bare "/orders" with a protocol error -- so the resolution happens
    here, and the first ` + "`goto`" + ` of a scenario needs a full URL for the same reason
    it does under the interpreter."""
    if urlparse(target).scheme:
        page.goto(target)
        return
    here = page.url
    if not here or here == "about:blank":
        raise ValueError(
            "goto %r is relative and the page has not been anywhere yet "
            "(the first goto of a scenario needs a full URL)" % (target,))
    page.goto(urljoin(here, target))
`,
}, {
	name:    helperRetry,
	imports: []string{"time"},
	src: `class art_attempt:
    """One attempt of a retried step. Entered as a context manager: a failure
    inside the block is swallowed on every attempt but the last, which raises."""

    def __init__(self, last):
        self.last = last
        self.failed = False

    def __enter__(self):
        return self

    def __exit__(self, kind, value, trace):
        self.failed = kind is not None
        return self.failed and not self.last


def art_retry(times=1, delay=0.0):
    """` + "`retry { times, delay }`" + `: one attempt per iteration, with the delay
    between two of them. The step's action and its assertions are both inside the
    block, so they retry together -- and a ` + "`capture`" + ` written after them stays in
    the test function's own scope rather than inside a nested function."""
    attempts = max(1, times)
    for n in range(attempts):
        if n:
            time.sleep(delay)
        attempt = art_attempt(last=n == attempts - 1)
        yield attempt
        if not attempt.failed:
            return
`,
}, {
	name:    helperWithin,
	imports: []string{"time"},
	src: `def art_within(ask, seconds):
    """` + "`within`" + ` on a browser assertion that has no native playwright expect()
    shape: re-ask it every 100ms until it holds or the budget expires, which is
    the settle loop pkg/steps/browserstep runs. The last try is unguarded, so a
    budget that runs out reports the real reason rather than a bare False."""
    deadline = time.monotonic() + seconds
    while True:
        last = time.monotonic() >= deadline
        try:
            if ask():
                return True
        except Exception:
            if last:
                raise
        else:
            if last:
                return False
        time.sleep(0.1)
`,
}, {
	name:    helperBrowser,
	imports: []string{"pytest", "playwright"},
	src: `@pytest.fixture
def browser():
    """One playwright page per test function, which is one scenario: the DSL
    gives a scenario one browser session and its steps share it.

    The fixture yields a factory rather than a page because ` + "`config browser`" + ` is
    written per scenario while a fixture is shared by the file. A test calls
    browser(headless=..., viewport=...) and gets its own page; everything opened
    is closed when the test ends."""
    opened = []
    with sync_playwright() as play:

        def open_page(headless=True, viewport=""):
            chrome = play.chromium.launch(headless=headless)
            opened.append(chrome)
            size = None
            if viewport:
                width, _, height = viewport.partition("x")
                size = {"width": int(width), "height": int(height)}
            return chrome.new_page(viewport=size)

        yield open_page
        for chrome in opened:
            chrome.close()
`,
}}

// byName is the table as a lookup, so use() is a map read.
var byName = func() map[string]helper {
	m := make(map[string]helper, len(helpers))
	for _, h := range helpers {
		m[h.name] = h
	}
	return m
}()

// importLines are the import statements, by the key a helper names.
//
// Playwright's two imports are one entry, because a file that needs a page needs
// both the driver and Playwright's own expect, and splitting them would let a
// file import one without the other.
var importLines = map[string]string{
	"base64":       "import base64",
	"datetime":     "import datetime",
	"email.utils":  "import email.utils",
	"hashlib":      "import hashlib",
	"hmac":         "import hmac",
	"json":         "import json",
	"math":         "import math",
	"os":           "import os",
	"random":       "import random",
	"re":           "import re",
	"urllib.parse": "import urllib.parse",
	"uuid":         "import uuid",
	"time":         "import time",
	"subprocess":   "import subprocess",
	"pytest":       "import pytest",
	"requests":     "import requests",
	"urllib":       "from urllib.parse import urljoin, urlparse",
	"playwright":   "from playwright.sync_api import expect, sync_playwright",
}

// importGroups are the imports in the order they are written: the standard
// library first, alphabetically, then the third-party ones, with a blank line
// between -- isort's grouping, which is what a linter in a Python repo would do
// to the file anyway.
var importGroups = [][]string{
	{"base64", "datetime", "email.utils", "hashlib", "hmac", "json", "math", "os", "random", "re", "subprocess", "time", "urllib", "urllib.parse", "uuid"},
	{"pytest", "requests", "playwright"},
}
