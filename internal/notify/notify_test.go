package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendPlain(t *testing.T) {
	var title, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title = r.Header.Get("Title")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL+"/unified", "Imported", "Severance S02E03"); err != nil {
		t.Fatal(err)
	}
	if title != "Imported" || body != "Severance S02E03" {
		t.Errorf("got title %q body %q", title, body)
	}
}

func TestSendErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL, "t", "m"); err == nil {
		t.Error("a 403 gave no error")
	}
	if err := Send(context.Background(), "ftp://example.com", "t", "m"); err == nil {
		t.Error("a non-http URL gave no error")
	}
}

func TestHeaderValue(t *testing.T) {
	if got := headerValue("Låg\nplats"); got != "=?UTF-8?B?TMOlZyBwbGF0cw==?=" {
		t.Errorf("headerValue = %q", got)
	}
}
