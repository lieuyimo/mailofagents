package httpclient

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type capturedHTTP struct {
	Method      string
	Path        string
	Auth        string
	ContentType string
	Body        map[string]any
}

// captureServer records the last request and replies with the given status
// and JSON body.
func captureServer(t *testing.T, status int, response string) (*httptest.Server, func() capturedHTTP) {
	t.Helper()
	var mu sync.Mutex
	var last capturedHTTP
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		mu.Lock()
		last = capturedHTTP{
			Method:      r.Method,
			Path:        r.URL.Path,
			Auth:        r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(ts.Close)
	return ts, func() capturedHTTP {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

func TestNewNormalizesBaseURL(t *testing.T) {
	if got := New("https://mail.example.com///").BaseURL(); got != "https://mail.example.com" {
		t.Fatalf("BaseURL = %q, want trailing slashes trimmed", got)
	}
}

func TestVerifyPasswordRequestShape(t *testing.T) {
	ts, last := captureServer(t, http.StatusOK, `{}`)
	if err := New(ts.URL).VerifyPassword("alice@test", "secret"); err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	got := last()
	if got.Method != http.MethodPost || got.Path != "/api/verify-password" {
		t.Fatalf("request = %s %s", got.Method, got.Path)
	}
	if got.Auth != "" {
		t.Fatalf("verify-password must not send auth, got %q", got.Auth)
	}
	if !strings.HasPrefix(got.ContentType, "application/json") {
		t.Fatalf("content-type = %q", got.ContentType)
	}
	if got.Body["address"] != "alice@test" || got.Body["password"] != "secret" {
		t.Fatalf("body = %+v", got.Body)
	}
}

func TestVerifyPasswordErrorMapping(t *testing.T) {
	ts, _ := captureServer(t, http.StatusUnauthorized, `{"error":"invalid credentials"}`)
	err := New(ts.URL).VerifyPassword("alice@test", "wrong")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *httpclient.Error", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", apiErr.Status)
	}
	if !strings.Contains(apiErr.Body, "invalid credentials") {
		t.Fatalf("body = %q", apiErr.Body)
	}
}

func TestRegisterRequestShape(t *testing.T) {
	ts, last := captureServer(t, http.StatusOK, `{"address":"alice@test"}`)
	res, err := New(ts.URL).Register("alice", "mypassword1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if res.Address != "alice@test" {
		t.Fatalf("response = %+v", res)
	}
	got := last()
	if got.Method != http.MethodPost || got.Path != "/api/register" {
		t.Fatalf("request = %s %s", got.Method, got.Path)
	}
	if got.Auth != "" {
		t.Fatalf("register must not send auth, got %q", got.Auth)
	}
	if got.Body["name"] != "alice" || got.Body["password"] != "mypassword1" {
		t.Fatalf("body = %+v", got.Body)
	}
}

func TestRegisterOmitsEmptyPassword(t *testing.T) {
	ts, last := captureServer(t, http.StatusOK, `{"address":"bob@test"}`)
	if _, err := New(ts.URL).Register("bob", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got := last()
	if _, ok := got.Body["password"]; ok {
		t.Fatalf("empty password must be omitted: %+v", got.Body)
	}
	if got.Body["name"] != "bob" {
		t.Fatalf("body = %+v", got.Body)
	}
}

func TestSendUsesFullAddressBasicAuth(t *testing.T) {
	ts, last := captureServer(t, http.StatusOK, `{"message_id":"msg-1"}`)
	res, err := New(ts.URL).Send(
		"alice@test", "secret",
		[]string{"bob@test"}, []string{"cc@test"},
		"subject", "body", true, []string{"file-1"}, "parent-1",
	)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.MessageID != "msg-1" {
		t.Fatalf("message id = %q", res.MessageID)
	}

	got := last()
	if got.Method != http.MethodPost || got.Path != "/api/send" {
		t.Fatalf("request = %s %s", got.Method, got.Path)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice@test:secret"))
	if got.Auth != wantAuth {
		t.Fatalf("auth = %q, want %q", got.Auth, wantAuth)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got.Auth, "Basic "))
	if err != nil {
		t.Fatalf("decode auth: %v", err)
	}
	if string(decoded) == "alice:secret" {
		t.Fatal("Basic auth used the short local-part; the server requires the full address")
	}

	if got.Body["subject"] != "subject" || got.Body["body"] != "body" {
		t.Fatalf("body = %+v", got.Body)
	}
	if got.Body["public"] != true || got.Body["in_reply_to"] != "parent-1" {
		t.Fatalf("body = %+v", got.Body)
	}
	if to, _ := got.Body["to"].([]any); len(to) != 1 || to[0] != "bob@test" {
		t.Fatalf("to = %+v", got.Body["to"])
	}
	if cc, _ := got.Body["cc"].([]any); len(cc) != 1 || cc[0] != "cc@test" {
		t.Fatalf("cc = %+v", got.Body["cc"])
	}
	if att, _ := got.Body["attachments"].([]any); len(att) != 1 || att[0] != "file-1" {
		t.Fatalf("attachments = %+v", got.Body["attachments"])
	}
}

func TestErrorBodyTruncated(t *testing.T) {
	ts, _ := captureServer(t, http.StatusInternalServerError, strings.Repeat("x", 1000))
	err := New(ts.URL).VerifyPassword("alice@test", "secret")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *httpclient.Error", err, err)
	}
	if len(apiErr.Body) != 300 {
		t.Fatalf("error body length = %d, want 300", len(apiErr.Body))
	}
}
