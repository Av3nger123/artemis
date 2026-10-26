package httpstep

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// echoServer answers with what it was sent: Content-Type, Content-Length, the
// raw body, and a form's fields and files when it was one.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"type": r.Header.Get("Content-Type"), "length": r.ContentLength}
		media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		switch media {
		case "multipart/form-data":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parsing multipart: %v", err)
			}
			out["form"] = r.MultipartForm.Value
			files := map[string]any{}
			for name, hs := range r.MultipartForm.File {
				f, _ := hs[0].Open()
				b, _ := io.ReadAll(f)
				f.Close()
				files[name] = map[string]any{"filename": hs[0].Filename, "body": string(b), "type": hs[0].Header.Get("Content-Type")}
			}
			out["files"] = files
		case "application/x-www-form-urlencoded":
			_ = r.ParseForm()
			out["form"] = r.PostForm
		default:
			b, _ := io.ReadAll(r.Body)
			out["body"] = string(b)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func send(t *testing.T, req models.Request) map[string]any {
	t.Helper()
	roots, err := Executor{}.Observe(context.Background(), models.Step{Name: "send", Request: req}, executor.NewScope())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	body, ok := roots[RootBody].(map[string]any)
	if !ok {
		t.Fatalf("the echo was not JSON: %v", roots[RootRaw])
	}
	return body
}

func fixtureFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A file as the body is its bytes, sent with the length the file has: not
// chunked, and not read into the request first.
func TestAFileBodyIsItsBytes(t *testing.T) {
	srv := echoServer(t)
	path := fixtureFile(t, "hello.txt", "hello\n")
	got := send(t, models.Request{Method: "PUT", URL: srv.URL, BodyFile: path,
		Headers: map[string]string{"Content-Type": "text/plain"}})
	if got["body"] != "hello\n" || got["length"] != float64(6) {
		t.Errorf("echo = %v, want the file's six bytes with a length of 6", got)
	}
}

// A form without files is urlencoded, in order, a repeated name keeping both.
func TestAFormIsURLEncoded(t *testing.T) {
	srv := echoServer(t)
	got := send(t, models.Request{Method: "POST", URL: srv.URL, Parts: []models.Part{
		{Name: "tag", Value: "a"}, {Name: "tag", Value: "b"}, {Name: "user", Value: "alice"},
	}})
	if got["type"] != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %v", got["type"])
	}
	form := got["form"].(map[string]any)
	if tags := form["tag"].([]any); len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tag = %v, want [a b] in order", form["tag"])
	}
}

// A multipart form carries text fields and files, each file under its base
// name with a type from its extension, and goes with a length.
func TestAMultipartFormCarriesFiles(t *testing.T) {
	srv := echoServer(t)
	path := fixtureFile(t, "notes.txt", "remember\n")
	got := send(t, models.Request{Method: "POST", URL: srv.URL, Multipart: true, Parts: []models.Part{
		{Name: "doc", File: path}, {Name: "who", Value: "alice"},
	}})
	if got["length"].(float64) <= 0 {
		t.Errorf("a multipart body went with no length: %v", got["length"])
	}
	doc := got["files"].(map[string]any)["doc"].(map[string]any)
	if doc["filename"] != "notes.txt" || doc["body"] != "remember\n" {
		t.Errorf("doc = %v", doc)
	}
	if typ, _, _ := mime.ParseMediaType(doc["type"].(string)); typ != "text/plain" {
		t.Errorf("doc's type = %v, want text/plain from its extension", doc["type"])
	}
	if who := got["form"].(map[string]any)["who"].([]any); who[0] != "alice" {
		t.Errorf("who = %v", who)
	}
}

// A body file that is not there is a step error naming it, not an empty body.
func TestAMissingBodyFileIsAnError(t *testing.T) {
	_, err := Executor{}.Observe(context.Background(), models.Step{Name: "send", Request: models.Request{
		Method: "PUT", URL: "http://127.0.0.1:1", BodyFile: filepath.Join(t.TempDir(), "gone.txt"),
	}}, executor.NewScope())
	if err == nil {
		t.Fatal("a missing body file was sent")
	}
}
