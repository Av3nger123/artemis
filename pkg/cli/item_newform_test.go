package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newAuth is auth.art in the new form: an item's inputs are the vars above its
// action, and what it writes is a var after it. orders takes no user, so a
// run of the whole collection passes user to login alone, and orders reads
// the token login wrote from the context.
const newAuth = `collection "auth" {
  request login {
    var user
    var password = "s3cret"
    var base

    post "${base}/token" {
      body = {"username": user, "password": password}
    }
    expect status == 200
    var token = body.data.access_token
  }

  request orders {
    var base
    var token

    get "${base}/orders" {
      header "Authorization" = "Bearer ${token}"
    }
    expect status == 200
  }
}
`

// --item writes a flow and a scenario file in memory -- flow item { step
// "auth.login" = auth.login { ... } } and scenario "auth.login" { } -- and the
// run passes against the server with the --arg values as the step's
// arguments. Nothing is written beside the collection.
func TestItemRunUsesTheNewForm(t *testing.T) {
	srv := authServer(t)
	dir := writeTree(t, t.TempDir(), map[string]string{"collections/auth.art": newAuth})
	file := filepath.Join(dir, "collections", "auth.art")
	captures := filepath.Join(dir, "captures.json")
	out, stderr, err := runCLI(t, "run", file, "--item", "auth.login",
		"--arg", "user=a", "--arg", "base="+srv.URL, "--report", "json", "--captures", captures)
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n%s", err, out, stderr)
	}
	for _, want := range []string{`"name": "auth.login"`, `"flow": "item"`, `"status": "pass"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the report has no %s:\n%s", want, out)
		}
	}
	if got := readCaptures(t, captures); got.Values["token"] != "t" {
		t.Errorf("token = %v, want t", got.Values["token"])
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "collections"))
	if len(entries) != 1 {
		t.Errorf("an item run writes nothing beside the collection, and the folder holds %v", entries)
	}
}

// A run of every item passes each argument to the items that declare it as
// an input, and the var one item writes reaches the next.
func TestItemRunOfACollectionPassesEachItemItsInputs(t *testing.T) {
	srv := authServer(t)
	dir := writeTree(t, t.TempDir(), map[string]string{"collections/auth.art": newAuth})
	out, stderr, err := runCLI(t, "run", filepath.Join(dir, "collections", "auth.art"), "--item", "auth",
		"--arg", "user=a", "--arg", "base="+srv.URL, "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n%s", err, out, stderr)
	}
	for _, want := range []string{`"auth.login"`, `"auth.orders"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the report has no step %s:\n%s", want, out)
		}
	}
}

// An argument the item has no input for is the checker's unknown-argument, at
// the step the in-memory flow wrote, and nothing is sent.
func TestItemRunRefusesAnArgumentTheItemDoesNotTake(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"collections/auth.art": newAuth})
	_, stderr, err := runCLI(t, "run", filepath.Join(dir, "collections", "auth.art"), "--item", "auth.login",
		"--arg", "user=a", "--arg", "base=http://127.0.0.1:1", "--arg", "pasword=x")
	if err == nil {
		t.Fatal("a run with an argument the item does not take passed")
	}
	if !strings.Contains(stderr, `has no parameter "pasword"`) || !strings.Contains(stderr, "auth.item.flow.art") {
		t.Errorf("want unknown-argument in the in-memory flow, got:\n%s", stderr)
	}
}
