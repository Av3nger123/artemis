package cli

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/lower"
)

// tagsFlag and grepFlag are the flags that select which scenarios a run runs.
const (
	tagsFlag = "tags"
	grepFlag = "grep"
)

// selector is what a run selects scenarios by: a tag expression, a pattern
// over their names, or both. A nil selector selects everything.
type selector struct {
	tags     tagExpr
	tagsText string
	grep     *regexp.Regexp
}

// newSelector builds a selector from --tags and --grep, or nil when neither
// was given. A mistake in either is an error before anything is discovered:
// a mistyped expression must not cost a suite run.
func newSelector(tags, grep string) (*selector, error) {
	if strings.TrimSpace(tags) == "" && grep == "" {
		return nil, nil
	}
	s := &selector{tagsText: strings.TrimSpace(tags)}
	if s.tagsText != "" {
		expr, err := parseTagExpr(s.tagsText)
		if err != nil {
			return nil, fmt.Errorf("--tags %q: %w", tags, err)
		}
		s.tags = expr
	}
	if grep != "" {
		re, err := regexp.Compile(grep)
		if err != nil {
			return nil, fmt.Errorf("--grep %q: %w", grep, err)
		}
		s.grep = re
	}
	return s, nil
}

// selects reports whether a scenario named name with tags is one to run.
func (s *selector) selects(name string, tags []string) bool {
	if s == nil {
		return true
	}
	if s.tags != nil {
		set := make(map[string]bool, len(tags))
		for _, t := range tags {
			set[t] = true
		}
		if !s.tags.match(set) {
			return false
		}
	}
	return s.grep == nil || s.grep.MatchString(name)
}

// reason is what an unselected scenario says, in the report.
func (s *selector) reason() string {
	var parts []string
	if s.tagsText != "" {
		parts = append(parts, fmt.Sprintf("--tags %q", s.tagsText))
	}
	if s.grep != nil {
		parts = append(parts, fmt.Sprintf("--grep %q", s.grep.String()))
	}
	return "not selected by " + strings.Join(parts, " and ")
}

// words is every tag the expression names, for the warning about one no
// scenario has.
func (s *selector) words() []string {
	if s == nil || s.tags == nil {
		return nil
	}
	var out []string
	s.tags.words(&out)
	return out
}

// warnUnknownTags writes a warning for each tag the expression names that no
// scenario in the run has: `--tags smok` selecting nothing because of a typo
// is otherwise indistinguishable from a suite with no smoke tests.
func (s *selector) warnUnknownTags(w io.Writer, known map[string]bool) {
	all := make([]string, 0, len(known))
	for t := range known {
		all = append(all, t)
	}
	sort.Strings(all)
	for _, word := range s.words() {
		if known[word] {
			continue
		}
		msg := fmt.Sprintf("warning: no scenario in this run is tagged %q", word)
		if best, near := diag.Nearest(word, all); near {
			msg += fmt.Sprintf("; did you mean %q?", best)
		}
		fmt.Fprintln(w, msg)
	}
}

// tagsOf is a scenario's tags as a run selects by them: its own and every
// suite's above it.
func (rt *runtimeEnv) tagsOf(file string, sc *lower.Scenario) []string {
	tags := append([]string(nil), sc.Tags...)
	for st := rt.suiteFor(file); st != nil; st = st.parent {
		if st.block != nil {
			tags = append(tags, st.block.Tags...)
		}
	}
	return tags
}

// tagExpr is a parsed --tags expression.
//
//	expr  = and { "or" and }
//	and   = unary { "and" unary }
//	unary = "not" unary | "(" expr ")" | TAG
type tagExpr interface {
	match(tags map[string]bool) bool
	words(out *[]string)
}

type tagWord string
type tagNot struct{ x tagExpr }
type tagAnd struct{ l, r tagExpr }
type tagOr struct{ l, r tagExpr }

func (t tagWord) match(tags map[string]bool) bool { return tags[string(t)] }
func (t tagNot) match(tags map[string]bool) bool  { return !t.x.match(tags) }
func (t tagAnd) match(tags map[string]bool) bool  { return t.l.match(tags) && t.r.match(tags) }
func (t tagOr) match(tags map[string]bool) bool   { return t.l.match(tags) || t.r.match(tags) }

func (t tagWord) words(out *[]string) { *out = append(*out, string(t)) }
func (t tagNot) words(out *[]string)  { t.x.words(out) }
func (t tagAnd) words(out *[]string)  { t.l.words(out); t.r.words(out) }
func (t tagOr) words(out *[]string)   { t.l.words(out); t.r.words(out) }

// parseTagExpr parses a --tags expression. `smoke` alone is the common case;
// `smoke and not slow` and `(api or ui) and smoke` are the rest.
func parseTagExpr(s string) (tagExpr, error) {
	p := &tagParser{toks: tagTokens(s)}
	x, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.i < len(p.toks) {
		return nil, fmt.Errorf("unexpected %q", p.toks[p.i])
	}
	return x, nil
}

func tagTokens(s string) []string {
	s = strings.NewReplacer("(", " ( ", ")", " ) ").Replace(s)
	return strings.Fields(s)
}

type tagParser struct {
	toks []string
	i    int
}

func (p *tagParser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *tagParser) or() (tagExpr, error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.peek() == "or" {
		p.i++
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		l = tagOr{l, r}
	}
	return l, nil
}

func (p *tagParser) and() (tagExpr, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	for p.peek() == "and" {
		p.i++
		r, err := p.unary()
		if err != nil {
			return nil, err
		}
		l = tagAnd{l, r}
	}
	return l, nil
}

func (p *tagParser) unary() (tagExpr, error) {
	switch t := p.peek(); t {
	case "":
		return nil, fmt.Errorf("a tag is missing at the end")
	case "not":
		p.i++
		x, err := p.unary()
		return tagNot{x}, err
	case "(":
		p.i++
		x, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.peek() != ")" {
			return nil, fmt.Errorf("a \"(\" is not closed")
		}
		p.i++
		return x, nil
	case ")", "and", "or":
		return nil, fmt.Errorf("expected a tag, found %q", t)
	default:
		p.i++
		return tagWord(t), nil
	}
}

// selectScenarios marks which scenario of each file the run runs, warns about
// a tag no scenario has, and returns the files that compiled with only the
// environment needs of what was selected -- what the check before the run
// counts. A suite with no selected scenario under it is marked unused.
func (rt *runtimeEnv) selectScenarios(attempts []fileAttempt, warn io.Writer) []compiled {
	known := map[string]bool{}
	for _, st := range rt.suites {
		st.used = false
		if st.block != nil {
			for _, t := range st.block.Tags {
				known[t] = true
			}
		}
	}
	var out []compiled
	chose := false
	for i := range attempts {
		a := &attempts[i]
		if !a.ok {
			continue
		}
		c := &a.compiled
		c.selected = make([]bool, len(c.scenarios))
		var needs []check.EnvNeed
		for j, sc := range c.scenarios {
			tags := rt.tagsOf(c.file, sc)
			for _, t := range tags {
				known[t] = true
			}
			if !rt.sel.selects(sc.Name, tags) {
				continue
			}
			c.selected[j] = true
			chose = true
			needs = append(needs, sc.EnvNeeds...)
			for st := rt.suiteFor(c.file); st != nil; st = st.parent {
				st.used = true
			}
		}
		out = append(out, compiled{file: c.file, needs: needs})
	}
	rt.sel.warnUnknownTags(warn, known)
	rt.noneSelected = !chose
	return out
}
