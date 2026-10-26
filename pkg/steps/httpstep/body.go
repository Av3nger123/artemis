package httpstep

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"artemis/pkg/shared/models"
)

// body is a request body ready to send: the reader, its length when it is
// known, how to read it again for a redirect, and the type it implies.
type body struct {
	reader      io.Reader
	length      int64 // -1 when unknown
	again       func() (io.ReadCloser, error)
	contentType string
	close       func()
}

// bodyOf builds the body a request says it has: text, a file, or a form.
//
// A file is opened here, per attempt, because a retry needs a reader from the
// start and a file read once is a body sent once. It is not read into memory:
// net/http streams it, with the length the file has, and closes it.
//
// A multipart form is built in memory instead. Its length is then known, so it
// goes with a Content-Length rather than chunked -- which a good share of
// upload endpoints refuse -- and a redirect can send it again. The cost is the
// form's size in memory, which for the fixtures a test uploads is nothing.
func bodyOf(r models.Request) (body, error) {
	switch {
	case r.BodyFile != "":
		f, err := os.Open(r.BodyFile)
		if err != nil {
			return body{}, fmt.Errorf("body: %w", err)
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return body{}, fmt.Errorf("body: %w", err)
		}
		return body{
			reader: f,
			length: info.Size(),
			again:  func() (io.ReadCloser, error) { return os.Open(r.BodyFile) },
			close:  func() { f.Close() },
		}, nil

	case len(r.Parts) > 0 && r.Multipart:
		b, contentType, err := multipartBody(r.Parts)
		if err != nil {
			return body{}, err
		}
		return fixed(b, contentType), nil

	case len(r.Parts) > 0:
		values := url.Values{}
		for _, p := range r.Parts {
			values.Add(p.Name, p.Value)
		}
		return fixed([]byte(values.Encode()), "application/x-www-form-urlencoded"), nil
	}
	return fixed([]byte(r.Body), ""), nil
}

func fixed(b []byte, contentType string) body {
	return body{
		reader:      bytes.NewReader(b),
		length:      int64(len(b)),
		again:       func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil },
		contentType: contentType,
	}
}

// multipartBody writes a multipart/form-data body: a text part per text field,
// and per file a part with the file's base name and a type from its extension.
func multipartBody(parts []models.Part) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		if p.File == "" {
			if err := w.WriteField(p.Name, p.Value); err != nil {
				return nil, "", fmt.Errorf("multipart %q: %w", p.Name, err)
			}
			continue
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
			quote(p.Name), quote(filepath.Base(p.File))))
		h.Set("Content-Type", fileType(p.File))
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", fmt.Errorf("multipart %q: %w", p.Name, err)
		}
		f, err := os.Open(p.File)
		if err != nil {
			return nil, "", fmt.Errorf("multipart %q: %w", p.Name, err)
		}
		_, err = io.Copy(part, f)
		f.Close()
		if err != nil {
			return nil, "", fmt.Errorf("multipart %q: %w", p.Name, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("multipart: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// quote escapes a name for a Content-Disposition parameter, as
// mime/multipart's own CreateFormFile does.
func quote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// fileType is a file's type from its extension, octet-stream when unknown.
func fileType(path string) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	return "application/octet-stream"
}
