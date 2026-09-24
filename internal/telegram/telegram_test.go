package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "123456789:AAE-secret_token"

func newTestClient(srv *httptest.Server) *Client {
	c := New(testToken, "-1001234567890", srv.Client())
	c.BaseURL = srv.URL
	return c
}

func TestSend(t *testing.T) {
	var (
		gotPath        string
		gotMethod      string
		gotContentType string
		gotBody        map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotContentType = r.URL.Path, r.Method, r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv).Send(context.Background(), "<b>Fear &amp; Greed</b>"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/bot"+testToken+"/sendMessage" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	want := map[string]any{
		"chat_id":              "-1001234567890",
		"text":                 "<b>Fear &amp; Greed</b>",
		"parse_mode":           "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	gotJSON, _ := json.Marshal(gotBody)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("body = %s, want %s", gotJSON, wantJSON)
	}
}

func TestSendAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()

	err := newTestClient(srv).Send(context.Background(), "hi")
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("err = %v, want the API description", err)
	}
}

func TestSendNonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>Bad Gateway</html>"))
	}))
	defer srv.Close()

	err := newTestClient(srv).Send(context.Background(), "hi")
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v, want HTTP 502 in it", err)
	}
}

func TestSendErrorsDoNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	c := newTestClient(srv)
	srv.Close() // connection refused: net/http reports the URL with the token

	err := c.Send(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("error leaks the bot token: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Errorf("error = %v, want the URL with a redacted token", err)
	}
}

func TestSendCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request must not be sent with a cancelled context")
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := newTestClient(srv).Send(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("error leaks the bot token: %v", err)
	}
}
