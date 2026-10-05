package codegen

// The JavaScript the generated module carries with it.
//
// The mirror of helpers.go, and every helper here exists for the same reason one
// exists there: the DSL has an operator JavaScript does not, or has one whose
// meaning differs, and the difference is pkg/eval's documented behaviour rather
// than an implementation detail. Each is written against the paragraph of
// pkg/eval that defines it, and against the Python helper that is the other
// spelling of the same rule -- so the two targets can be read side by side and a
// rule in one and not the other is visible.
//
// They are emitted on demand and in table order, so a file of api scenarios
// carries no settle loop and no browser launcher, and two runs over the same
// file produce the same bytes.

// The helper keys, which are also the JavaScript names. They are the Python
// names too wherever the helper is the same rule, because a reader who knows one
// export then knows the other.
const (
	jsHelperRender   = "art_render"
	jsHelperEnv      = "art_env"
	jsHelperJSON     = "art_json"
	jsHelperURL      = "art_url"
	jsHelperAt       = "art_at"
	jsHelperEq       = "art_eq"
	jsHelperSeconds  = "art_seconds"
	jsHelperExists   = "art_exists"
	jsHelperIs       = "art_is"
	jsHelperContains = "art_contains"
	jsHelperRegExp   = "art_regexp"
	jsHelperMatches  = "art_matches"
	jsHelperMatch    = "art_match"
	jsHelperText     = "art_text"
	jsHelperValue    = "art_value"
	jsHelperAttr     = "art_attr"
	jsHelperExit     = "art_exit"
	jsHelperArgv     = "art_argv"
	jsHelperGoto     = "art_goto"
	jsHelperRetry    = "art_retry"
	jsHelperWithin   = "art_within"
	jsHelperBrowser  = "art_browser"
)

// jsHelper is one emitted definition: its JavaScript source, the imported
// bindings it needs and the other helpers it calls.
//
// imports are `module.member` pairs rather than module names, because a
// JavaScript import names what it brings in: a helper that calls onTestFinished
// needs that one binding from vitest and not the module.
type jsHelper struct {
	name    string
	imports []string
	needs   []string
	src     string
}

// jsHelpers are in emission order. Nothing here is alphabetical: art_render
// comes first because most of the others call it, and art_browser comes last
// because it is the one that opens something.
var jsHelpers = []jsHelper{{
	name: jsHelperRender,
	src: `function art_render(value) {
  // Render a value the way artemis does when it goes into a string: a whole
  // number without a trailing .0, a boolean as true or false, null as "null",
  // and an array or an object as compact JSON. This is eval.Render, which is
  // what makes a URL built here and a URL built by ` + "`artemis run`" + ` the same string.
  if (value === null || value === undefined) return "null";
  if (typeof value === "string") return value;
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") return String(value);
  return JSON.stringify(value);
}
`,
}, {
	name: jsHelperEnv,
	src: `function art_env(name, fallback) {
  // Read an environment variable the way artemis does. A variable that is not
  // set, that is empty, or that holds only space characters has no value: with
  // no fallback this throws, rather than giving the empty string that used to
  // go straight into a URL. This is eval.Env.getenv, and the two must agree.
  const value = process.env[name];
  if (value !== undefined && String(value).trim() !== "") return value;
  if (fallback !== undefined) return fallback;
  throw new Error(` + "`the environment variable ${name} has no value`" + `);
}
`,
}, {
	name: jsHelperJSON,
	src: `function art_json(text) {
  // What an api step binds to ` + "`body`" + `: the response decoded as any JSON value,
  // or null when it is not JSON. A body that is not JSON is not a failure --
  // whether the step needed one is what its asserts say.
  //
  // Over the text rather than response.json(), because a fetch body is a stream
  // that can be read once and ` + "`raw`" + ` needs it too.
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}
`,
}, {
	name: jsHelperURL,
	src: `function art_url(base, pairs) {
  // A request's query parameters, appended in source order.
  //
  // fetch has no params option, so the URL is built here. A list of pairs rather
  // than an object, because source order is kept and a repeated ` + "`query \"tag\"`" + `
  // keeps both values -- which an object would silently collapse.
  // URLSearchParams does the percent-escaping lower.queryString does by hand.
  const url = new URL(base);
  for (const [name, value] of pairs) url.searchParams.append(name, value);
  return url.href;
}
`,
}, {
	name: jsHelperAt,
	src: `function art_at(root, ...path) {
  // A path read: ` + "`body.meta.owner.name`" + ` and ` + "`body.items[0]`" + ` both arrive here as
  // one call with the whole path, so a deep read is one line and not four
  // nested ones.
  //
  // It throws on a key that is not there, which is the whole reason it exists.
  // JavaScript's own ` + "`body.missing`" + ` is undefined, so ` + "`expect body.missing is null`" + `
  // would hold here and fail under both the interpreter and the generated
  // pytest -- where the subscript raises KeyError. An unresolvable path is an
  // errored assertion in all three.
  let value = root;
  for (const key of path) {
    if (value instanceof Headers) {
      // The ` + "`headers`" + ` root is fetch's own Headers, whose get() is
      // case-insensitive -- which is the lookup eval.Headers does.
      if (!value.has(key)) throw new Error(` + "`there is no header ${JSON.stringify(key)}`" + `);
      value = value.get(key);
      continue;
    }
    if (value === null || value === undefined || typeof value !== "object") {
      throw new TypeError(` + "`${JSON.stringify(key)} cannot be read off ${art_render(value)}`" + `);
    }
    if (Array.isArray(value)) {
      if (typeof key !== "number" || key < 0 || key >= value.length) {
        throw new Error(` + "`there is no element ${art_render(key)}`" + `);
      }
    } else if (!Object.hasOwn(value, key)) {
      throw new Error(` + "`there is no ${JSON.stringify(String(key))}`" + `);
    }
    value = value[key];
  }
  return value;
}
`,
	needs: []string{jsHelperRender},
}, {
	name: jsHelperEq,
	src: `function art_eq(a, b) {
  // Equality for a comparison with no scalar literal on either side.
  //
  // Structural, because Python's ` + "`==`" + ` is -- so the two exports answer the same
  // thing about two arrays, where JavaScript's own ` + "`===`" + ` would compare
  // references. A comparison against a literal is emitted as ` + "`===`" + ` and never
  // reaches this.
  if (a === b) return true;
  if (a === null || a === undefined || b === null || b === undefined) {
    return (a ?? null) === (b ?? null);
  }
  if (typeof a !== typeof b || typeof a !== "object") return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  if (Array.isArray(a)) {
    return a.length === b.length && a.every((e, i) => art_eq(e, b[i]));
  }
  const keys = Object.keys(a);
  return (
    keys.length === Object.keys(b).length &&
    keys.every((k) => Object.hasOwn(b, k) && art_eq(a[k], b[k]))
  );
}
`,
}, {
	name: jsHelperSeconds,
	src: `function art_seconds(text) {
  // A duration the way the DSL writes it -- "500ms", "5s", "1m30s" -- as a
  // number of seconds. A ` + "`timeout`" + `, a retry ` + "`delay`" + ` or a ` + "`within`" + ` budget may come
  // out of an expression, so the parsing has to happen at run time too; a
  // literal one is converted when the file is generated and never reaches this.
  const units = { ns: 1e-9, us: 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
  const found = [...String(text).matchAll(/([0-9]*\.?[0-9]+)(ns|us|ms|s|m|h)/g)];
  if (found.length === 0) {
    throw new Error(
      ` + "`${JSON.stringify(text)} is not a duration (want something like \"500ms\" or \"5s\")`" + `,
    );
  }
  return found.reduce((total, [, n, u]) => total + Number(n) * units[u], 0);
}
`,
}, {
	name: jsHelperExists,
	src: `async function art_exists(read) {
  // ` + "`expr exists`" + `: false for a path that does not resolve and false for a JSON
  // null, which is eval's rule -- a null is treated as absent, and ` + "`exists`" + ` is
  // the operator for asking rather than an error.
  //
  // The read is a function because an absent path throws out of art_at:
  // evaluating it before the call would throw where this has to answer false.
  // Async because the operand may be an element read, which is a promise on
  // this side of the export and was synchronous on the Python one.
  try {
    return (await read()) != null;
  } catch {
    return false;
  }
}
`,
}, {
	name: jsHelperIs,
	src: `function art_type(value) {
  // A value's JSON type, in the names ` + "`is`" + ` accepts.
  if (value === null || value === undefined) return "null";
  if (typeof value === "boolean") return "boolean";
  if (typeof value === "string") return "string";
  if (typeof value === "number") return "number";
  if (Array.isArray(value)) return "array";
  if (typeof value === "object") return "object";
  return "unknown";
}

function art_is(value, want) {
  // ` + "`expr is <type>`" + `, over the six JSON type names.
  return art_type(value) === want;
}
`,
}, {
	name:  jsHelperContains,
	needs: []string{jsHelperRender, jsHelperEq},
	src: `function art_contains(haystack, needle) {
  // ` + "`contains`" + `: a substring of a string, an element of an array, or a key of an
  // object. The three readings come from the left operand's type, which is
  // eval's rule; a left operand of any other type is a mistake with no honest
  // false, so it throws.
  if (typeof haystack === "string") {
    if (needle === null || needle === undefined || typeof needle === "object") {
      throw new TypeError("contains against a string needs a string");
    }
    return haystack.includes(art_render(needle));
  }
  if (Array.isArray(haystack)) return haystack.some((e) => art_eq(e, needle));
  if (haystack instanceof Headers) return haystack.has(art_render(needle));
  if (haystack !== null && typeof haystack === "object") {
    return Object.hasOwn(haystack, needle);
  }
  throw new TypeError("contains needs a string, array or object on the left");
}
`,
}, {
	name: jsHelperRegExp,
	src: `function art_regexp(pattern) {
  // A pattern position takes a regex literal or a string -- the rule ` + "`matches`" + `
  // and ` + "`match()`" + ` already follow, so a pattern can live in a var. A literal is
  // emitted as a RegExp and arrives here as one.
  return pattern instanceof RegExp ? pattern : new RegExp(pattern);
}
`,
}, {
	name:  jsHelperMatches,
	needs: []string{jsHelperRender, jsHelperRegExp},
	src: `function art_matches(subject, pattern) {
  // ` + "`matches`" + `: an unanchored search, as a regex usually is. A composite or a
  // null has no rendering worth matching and throws rather than matching
  // "null".
  if (subject === null || subject === undefined || typeof subject === "object") {
    throw new TypeError("matches needs a string on the left");
  }
  return art_regexp(pattern).test(art_render(subject));
}
`,
}, {
	name:  jsHelperMatch,
	needs: []string{jsHelperRender, jsHelperRegExp},
	src: `function art_match(subject, pattern) {
  // ` + "`match(text, /re/)`" + `: capturing group 1 when the pattern has one and the
  // whole match when it has none. Two groups is a mistake rather than a choice,
  // and a pattern that matched nothing is an error rather than an empty string
  // -- the text was there and the question was asked. The value is always a
  // string, because reading "007" as seven loses data.
  const re = art_regexp(pattern);
  // JavaScript has no RegExp.groups, so the count comes from a pattern that
  // matches the empty string: the source with an empty alternative after it
  // always matches, and the result's length is one more than the group count.
  const groups = new RegExp(` + "`${re.source}|`" + `).exec("").length - 1;
  if (groups > 1) {
    throw new Error(
      ` + "`pattern ${re} has ${groups} capturing groups; match() takes one or none`" + `,
    );
  }
  const found = re.exec(art_render(subject));
  if (found === null) throw new Error(` + "`pattern ${re} matched nothing`" + `);
  return groups === 1 ? found[1] : found[0];
}
`,
}, {
	name: jsHelperText,
	src: `async function art_text(page, selector) {
  // ` + "`text(sel)`" + `: the first match's text content, or null for no match.
  //
  // count() on the first locator, not page.textContent(sel): the second
  // auto-waits and then throws, which would make ` + "`expect text(\".x\") is null`" + ` a
  // thirty-second assertion and a ` + "`within`" + ` budget a fiction.
  const found = page.locator(selector).first();
  if ((await found.count()) === 0) return null;
  return found.textContent();
}
`,
}, {
	name: jsHelperValue,
	src: `async function art_value(page, selector) {
  // ` + "`value(sel)`" + `: the first match's value as a form control, or null for no
  // match.
  const found = page.locator(selector).first();
  if ((await found.count()) === 0) return null;
  return found.inputValue();
}
`,
}, {
	name: jsHelperAttr,
	src: `async function art_attr(page, selector, name) {
  // ` + "`attr(sel, name)`" + `: the named attribute, null for an absent attribute and
  // null for no element -- the same answer by design, because ` + "`exists`" + ` is the
  // operator for telling them apart.
  const found = page.locator(selector).first();
  if ((await found.count()) === 0) return null;
  return found.getAttribute(name);
}
`,
}, {
	name: jsHelperExit,
	src: `function art_exit(proc) {
  // What a terminal step binds to ` + "`exit_code`" + `.
  //
  // spawnSync reports a timeout and a command that could not be started the
  // same way, as an ` + "`error`" + ` rather than a status, and both are step errors and
  // not observations -- which is what subprocess.run raises on the Python side.
  // A command that ran and exited non-zero has a status and reaches no throw:
  // ` + "`expect exit_code == 0`" + ` is what decides whether the scenario minds.
  if (proc.error) throw proc.error;
  if (proc.status === null) {
    throw new Error(` + "`the command was killed by ${proc.signal}`" + `);
  }
  return proc.status;
}
`,
}, {
	name:  jsHelperArgv,
	needs: []string{jsHelperRender},
	src: `function art_argv(args) {
  // The arguments a terminal step runs, every one rendered. There is no word
  // splitting anywhere, which is the whole reason ` + "`args`" + ` is a list rather than a
  // string -- and spawnSync is called with shell: false, which is its default.
  if (args === null || args === undefined) return [];
  if (!Array.isArray(args)) {
    throw new TypeError('args is not an array like ["-f", "seed.sql"]');
  }
  return args.map((a) => art_render(a));
}
`,
}, {
	name: jsHelperGoto,
	src: `async function art_goto(page, target) {
  // ` + "`goto`" + `: a relative target resolves against where the page already is.
  //
  // That is artemis's rule and not playwright's -- a context with no base URL
  // answers a bare "/orders" with a protocol error -- so the resolution happens
  // here, and the first ` + "`goto`" + ` of a scenario needs a full URL for the same reason
  // it does under the interpreter.
  if (/^[A-Za-z][A-Za-z0-9+.-]*:/.test(target)) {
    await page.goto(target);
    return;
  }
  const here = page.url();
  if (!here || here === "about:blank") {
    throw new Error(
      ` + "`goto ${JSON.stringify(target)} is relative and the page has not been anywhere `" + ` +
        "yet (the first goto of a scenario needs a full URL)",
    );
  }
  await page.goto(new URL(target, here).href);
}
`,
}, {
	name: jsHelperRetry,
	src: `async function art_retry(options, attempt) {
  // ` + "`retry { times, delay }`" + `: one call of the attempt per iteration, with the
  // delay between two of them, and the last failure rethrown.
  //
  // The step's action and its assertions are both inside the callback, so they
  // retry together. A ` + "`capture`" + ` written after them is assigned rather than
  // declared, so it outlives the callback and the steps below can read it --
  // which is why every binding a test writes is declared in one ` + "`let`" + ` at the top.
  const times = Math.max(1, options.times ?? 1);
  for (let n = 0; n < times; n++) {
    if (n) await new Promise((wake) => setTimeout(wake, (options.delay ?? 0) * 1000));
    try {
      await attempt();
      return;
    } catch (failure) {
      if (n === times - 1) throw failure;
    }
  }
}
`,
}, {
	name: jsHelperWithin,
	src: `async function art_within(ask, seconds) {
  // ` + "`within`" + ` on a browser assertion that has no native playwright expect()
  // shape: re-ask it every 100ms until it holds or the budget expires, which is
  // the settle loop pkg/steps/browserstep runs. The last try is unguarded, so a
  // budget that runs out reports the real reason rather than a bare false.
  const deadline = performance.now() + seconds * 1000;
  for (;;) {
    const last = performance.now() >= deadline;
    try {
      if (await ask()) return true;
      if (last) return false;
    } catch (failure) {
      if (last) throw failure;
    }
    await new Promise((wake) => setTimeout(wake, 100));
  }
}
`,
}, {
	name:    jsHelperBrowser,
	imports: []string{"@playwright/test.chromium", "vitest.onTestFinished"},
	src: `async function art_browser(options = {}) {
  // One playwright page per test, which is one scenario: the DSL gives a
  // scenario one browser session and its steps share it.
  //
  // vitest has no fixtures, so the browser is closed through onTestFinished --
  // vitest's own cleanup hook -- rather than a try/finally that would indent
  // every step of the scenario. A setting the scenario did not write is left off
  // the call, so "the scenario said nothing" stays different from "the scenario
  // said false".
  const chrome = await chromium.launch({ headless: options.headless ?? true });
  onTestFinished(() => chrome.close());
  let viewport = null;
  if (options.viewport) {
    const [width, height] = String(options.viewport).split("x");
    viewport = { width: Number(width), height: Number(height) };
  }
  return chrome.newPage({ viewport });
}
`,
}}

// jsByName is the table as a lookup, so jsUse() is a map read.
var jsByName = func() map[string]jsHelper {
	m := make(map[string]jsHelper, len(jsHelpers))
	for _, h := range jsHelpers {
		m[h.name] = h
	}
	return m
}()

// jsImportGroups are the modules in the order their imports are written: node's
// own builtins first, then the packages, with a blank line between -- which is
// what a linter in a JavaScript repo would do to the file anyway.
//
// `expect` is not in any group, because where it comes from depends on the file:
// vitest's for a module with no browser step, and @playwright/test's for one
// with. See jsFile.module.
var jsImportGroups = [][]string{
	{"node:child_process"},
	{"vitest", "@playwright/test"},
}

// jsImportable is every `module.member` a helper or an emitted statement may
// name. A member with no module in jsImportGroups would never be written, which
// is what jshelpers_test.go checks.
var jsImportable = words(
	"node:child_process.spawnSync",
	"vitest.test", "vitest.expect", "vitest.onTestFinished",
	"@playwright/test.chromium", "@playwright/test.expect",
)
