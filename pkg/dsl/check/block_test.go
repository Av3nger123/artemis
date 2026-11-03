package check

import (
	"strings"
	"testing"
)

// What a block lets the steps after it read. pkg/dsl/testdata/invalid's
// flow_blocks.art pins what it refuses; this pins what it allows.
func TestBlockScopes(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"every parallel branch's capture after the block", `
  parallel {
    step "a" {
      get "x"
      capture a = body.a
    }
    step "b" {
      get "y"
      capture b = body.b
    }
  }
  step "c" { get "${a}/${b}" }`},
		{"a capture every if branch makes", `
  if 1 == 1 {
    step "a" {
      get "x"
      capture id = body.id
    }
  } else {
    step "b" {
      get "y"
      capture id = body.id
    }
  }
  step "c" { get "${id}" }`},
		{"a capture every switch case and the default make", `
  switch 1 {
    case 1 { step "a" {
      get "x"
      capture id = body.id
    } }
    default { step "b" {
      get "y"
      capture id = body.id
    } }
  }
  step "c" { get "${id}" }`},
		{"a try's captures after it", `
  try {
    step "a" {
      get "x"
      capture id = body.id
    }
  } finally {
    step "b" { get "${id}" }
  }
  step "c" { get "${id}" }`},
		{"teardown reads a capture only one branch makes", `
  if 1 == 1 {
    step "a" {
      get "x"
      capture id = body.id
    }
  }
  teardown {
    step "b" { delete "${id}" }
  }`},
		{"until reads the body's captures, and so does the step after", `
  repeat max 3 {
    step "a" {
      get "x"
      capture state = body.state
    }
  } until state == "done"
  step "b" { get "${state}" }`},
		{"a name an if branch overwrites is bound after the block", `
  var code = 400
  if code == 400 {
    step "a" {
      get "x"
      capture code = status
    }
  }
  step "b" { get "${code}" }`},
		{"a name a catch overwrites is bound after the block", `
  step "a" {
    get "x"
    capture id = body.id
  }
  try {
    step "b" { get "y" }
  } catch {
    step "c" {
      get "z"
      capture id = body.id
    }
  }
  step "d" { get "${id}" }`},
		{"a name a parallel branch overwrites is bound after the block", `
  var code = 400
  parallel {
    step "a" {
      get "x"
      capture code = status
    }
    step "b" { get "y" }
  }
  step "c" { get "${code}" }`},
		{"a repeat overwrites a name each round", `
  var state = "new"
  repeat max 3 {
    step "a" {
      get "x/${state}"
      capture state = body.state
    }
  } until state == "done"
  step "b" { get "${state}" }`},
		{"blocks nest", `
  parallel {
    if 1 == 1 {
      step "a" { get "x" }
    }
    repeat within "1m" {
      step "b" {
      get "y"
      capture s = body.s
    }
    } until s == 1
  }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, bag := checkSource(t, "scenario \"s\" {"+c.body+"\n}\n")
			if bag.HasErrors() {
				var msgs []string
				for _, d := range bag.All() {
					msgs = append(msgs, d.Message)
				}
				t.Errorf("errors: %s", strings.Join(msgs, "; "))
			}
		})
	}
}
