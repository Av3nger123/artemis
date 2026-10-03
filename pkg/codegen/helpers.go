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
	helperSeconds  = "art_seconds"
	helperJSON     = "art_json"
	helperExists   = "art_exists"
	helperIs       = "art_is"
	helperContains = "art_contains"
	helperMatches  = "art_matches"
	helperMatch    = "art_match"
	helperText     = "art_text"
	helperValue    = "art_value"
	helperAttr     = "art_attr"
	helperArgv     = "art_argv"
	helperGoto     = "art_goto"
	helperRetry    = "art_retry"
	helperWithin   = "art_within"
	helperBrowser  = "browser"
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
	"json":       "import json",
	"os":         "import os",
	"re":         "import re",
	"time":       "import time",
	"subprocess": "import subprocess",
	"pytest":     "import pytest",
	"requests":   "import requests",
	"urllib":     "from urllib.parse import urljoin, urlparse",
	"playwright": "from playwright.sync_api import expect, sync_playwright",
}

// importGroups are the imports in the order they are written: the standard
// library first, alphabetically, then the third-party ones, with a blank line
// between -- isort's grouping, which is what a linter in a Python repo would do
// to the file anyway.
var importGroups = [][]string{
	{"json", "os", "re", "subprocess", "time", "urllib"},
	{"pytest", "requests", "playwright"},
}
