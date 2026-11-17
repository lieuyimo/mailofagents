package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordedRequest is one fake-server hit, captured for wire assertions.
type recordedRequest struct {
	Method string
	Path   string
	Auth   string
	Body   map[string]any
}

// fakeMailServer is a minimal /api stand-in. It records every request and
// returns just enough JSON for the gateway's client methods.
type fakeMailServer struct {
	mu           sync.Mutex
	verifyStatus int
	requests     []recordedRequest
}

func newFakeMailServer(t *testing.T) (*httptest.Server, *fakeMailServer) {
	t.Helper()
	fs := &fakeMailServer{verifyStatus: http.StatusOK}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		fs.mu.Lock()
		status := fs.verifyStatus
		fs.requests = append(fs.requests, recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Auth:   r.Header.Get("Authorization"),
			Body:   body,
		})
		fs.mu.Unlock()

		switch r.URL.Path {
		case "/api/verify-password":
			if status >= 400 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"invalid credentials"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/api/register":
			name, _ := body["name"].(string)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"address":%q}`, name+"@test")
		case "/api/send":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message_id":"msg-1"}`))
		case "/api/inbox":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"messages":[],"count":0}`))
		case "/api/subs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"subordinates":[],"superiors":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	t.Cleanup(ts.Close)
	return ts, fs
}

func (f *fakeMailServer) setVerifyStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifyStatus = code
}

func (f *fakeMailServer) snapshot() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

func (f *fakeMailServer) requestsTo(path string) []recordedRequest {
	var out []recordedRequest
	for _, req := range f.snapshot() {
		if req.Path == path {
			out = append(out, req)
		}
	}
	return out
}

func newGatewayForTest(t *testing.T, baseURL string) *Server {
	t.Helper()
	s := New(baseURL)
	s.out = io.Discard
	return s
}

// callTool exercises one tool through the same dispatch path MCP uses.
func callTool(t *testing.T, s *Server, name string, args map[string]any) rpcResponse {
	t.Helper()
	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	params, err := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(rawArgs)})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return s.handleToolsCall(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  params,
	})
}

// toolText unwraps a tool result into (text, isError).
func toolText(t *testing.T, resp rpcResponse) (string, bool) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("rpc error: %+v", resp.Error)
	}
	res, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result has unexpected type %T", resp.Result)
	}
	content, ok := res["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result content malformed: %+v", res["content"])
	}
	text, _ := content[0]["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func authenticateTool(t *testing.T, s *Server, address, password string) string {
	t.Helper()
	text, isErr := toolText(t, callTool(t, s, "authenticate", map[string]any{
		"address": address, "password": password,
	}))
	if isErr {
		t.Fatalf("authenticate failed: %s", text)
	}
	var out struct {
		AccessCode string `json:"access_code"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode authenticate result: %v (%s)", err, text)
	}
	if out.AccessCode == "" {
		t.Fatalf("authenticate returned an empty access code: %s", text)
	}
	return out.AccessCode
}

func TestIssueCodeRejectsBadPasswordAndMintsBoundEntry(t *testing.T) {
	ts, fs := newFakeMailServer(t)
	s := newGatewayForTest(t, ts.URL)

	fs.setVerifyStatus(http.StatusUnauthorized)
	if _, err := s.issueCode(context.Background(), "alice@test", "wrong", ""); err == nil ||
		!strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("bad password should fail authentication, got %v", err)
	}
	if len(s.codes) != 0 {
		t.Fatalf("failed authentication must not store a code, got %d", len(s.codes))
	}

	fs.setVerifyStatus(http.StatusOK)
	code, err := s.issueCode(context.Background(), "alice@test", "secret", "")
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	if len(code) != 64 {
		t.Fatalf("access code = %q, want 64 hex chars", code)
	}
	second, err := s.issueCode(context.Background(), "alice@test", "secret", "")
	if err != nil {
		t.Fatalf("second issueCode: %v", err)
	}
	if code == second {
		t.Fatal("two authenticate calls returned the same access code")
	}

	entry := s.codes[code]
	if entry == nil {
		t.Fatalf("code %q missing from the in-memory map", code)
	}
	if entry.Address != "alice@test" || entry.Password != "secret" {
		t.Fatalf("entry credentials = %+v", entry)
	}
	if entry.ServerURL != ts.URL {
		t.Fatalf("entry server = %q, want %q", entry.ServerURL, ts.URL)
	}
	if entry.CallsUsed != 0 || entry.MaxCalls != CodeMaxCalls {
		t.Fatalf("entry budget = %d/%d, want 0/%d", entry.CallsUsed, entry.MaxCalls, CodeMaxCalls)
	}
	if left := time.Until(entry.ExpiresAt); left < CodeTTL-time.Minute || left > CodeTTL+time.Minute {
		t.Fatalf("entry TTL = %v, want ~%v", left, CodeTTL)
	}

	for _, req := range fs.requestsTo("/api/verify-password") {
		if req.Method != http.MethodPost || req.Auth != "" {
			t.Fatalf("verify request shape = %+v", req)
		}
		if req.Body["address"] != "alice@test" {
			t.Fatalf("verify body = %+v", req.Body)
		}
	}
}

func TestIssueCodeBindsServerAndRoutesCalls(t *testing.T) {
	ts1, fs1 := newFakeMailServer(t)
	ts2, fs2 := newFakeMailServer(t)
	s := newGatewayForTest(t, ts1.URL)

	// Trailing slash must normalize to the same cached client.
	if s.getClient(ts2.URL) != s.getClient(ts2.URL+"/") {
		t.Fatal("trailing-slash URL should reuse the cached client")
	}

	code, err := s.issueCode(context.Background(), "bob@test", "pw", ts2.URL+"/")
	if err != nil {
		t.Fatalf("issueCode against second server: %v", err)
	}
	entry := s.codes[code]
	if entry.ServerURL != ts2.URL {
		t.Fatalf("entry server = %q, want %q", entry.ServerURL, ts2.URL)
	}

	text, isErr := toolText(t, callTool(t, s, "send_email", map[string]any{
		"access_code": code,
		"to":          []string{"carol@test"},
		"subject":     "hi",
		"body":        "hello",
	}))
	if isErr {
		t.Fatalf("send failed: %s", text)
	}

	if n := len(fs1.requestsTo("/api/send")); n != 0 {
		t.Fatalf("default server received %d sends, want 0", n)
	}
	sends := fs2.requestsTo("/api/send")
	if len(sends) != 1 {
		t.Fatalf("bound server received %d sends, want 1", len(sends))
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("bob@test:pw"))
	if sends[0].Auth != wantAuth {
		t.Fatalf("send auth = %q, want full-address Basic %q", sends[0].Auth, wantAuth)
	}
}

func TestConsumeCodeBudgetExpiryAndEviction(t *testing.T) {
	s := newGatewayForTest(t, "http://unused.example")
	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }

	s.codes["budget"] = &codeEntry{ExpiresAt: now.Add(time.Minute), MaxCalls: 2}
	for i := 0; i < 2; i++ {
		if _, err := s.consumeCode("budget"); err != nil {
			t.Fatalf("consume %d: %v", i, err)
		}
	}
	if _, err := s.consumeCode("budget"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("exhausted code = %v, want ErrInvalidCode", err)
	}
	if _, ok := s.codes["budget"]; ok {
		t.Fatal("exhausted code should be evicted")
	}

	s.codes["expired"] = &codeEntry{ExpiresAt: now.Add(-time.Second), MaxCalls: 5}
	if _, err := s.consumeCodeReadOnly("expired"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expired code = %v, want ErrInvalidCode", err)
	}
	if _, ok := s.codes["expired"]; ok {
		t.Fatal("expired code should be evicted")
	}

	if _, err := s.consumeCode("unknown"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("unknown code = %v, want ErrInvalidCode", err)
	}

	s.codes["read"] = &codeEntry{ExpiresAt: now.Add(time.Minute), MaxCalls: 1}
	for i := 0; i < 100; i++ {
		if _, err := s.consumeCodeReadOnly("read"); err != nil {
			t.Fatalf("read-only call %d: %v", i, err)
		}
	}
	if got := s.codes["read"].CallsUsed; got != 0 {
		t.Fatalf("read-only calls consumed %d budget slots, want 0", got)
	}
}

func TestConsumeCodeConcurrentBudget(t *testing.T) {
	s := newGatewayForTest(t, "http://unused.example")
	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }
	s.codes["concurrent"] = &codeEntry{ExpiresAt: now.Add(time.Hour), MaxCalls: 50}

	var ok, failed int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.consumeCode("concurrent"); err == nil {
				atomic.AddInt64(&ok, 1)
			} else {
				atomic.AddInt64(&failed, 1)
			}
		}()
	}
	wg.Wait()

	if ok != 50 || failed != 150 {
		t.Fatalf("budget under contention = %d ok / %d failed, want 50 / 150", ok, failed)
	}
	if _, exists := s.codes["concurrent"]; exists {
		t.Fatal("exhausted concurrent code should be evicted")
	}
}

func TestRegisterAndAuthenticateValidation(t *testing.T) {
	ts, fs := newFakeMailServer(t)
	s := newGatewayForTest(t, ts.URL)

	text, isErr := toolText(t, callTool(t, s, "register", map[string]any{"name": "   "}))
	if !isErr || !strings.Contains(text, "name is required") {
		t.Fatalf("blank name = %q, isError=%v", text, isErr)
	}
	if n := len(fs.requestsTo("/api/register")); n != 0 {
		t.Fatalf("blank name reached the server %d times, want 0", n)
	}

	text, isErr = toolText(t, callTool(t, s, "register", map[string]any{"name": "alice"}))
	if isErr {
		t.Fatalf("register failed: %s", text)
	}
	var reg struct {
		Address  string `json:"address"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(text), &reg); err != nil {
		t.Fatalf("decode register result: %v", err)
	}
	if reg.Address != "alice@test" || len(reg.Password) < 8 {
		t.Fatalf("register result = %+v", reg)
	}
	regs := fs.requestsTo("/api/register")
	if len(regs) != 1 || regs[0].Body["password"] != reg.Password {
		t.Fatalf("register request did not carry the returned password: %+v", regs)
	}

	text, isErr = toolText(t, callTool(t, s, "register", map[string]any{
		"name": "bob", "password": "mypassword1",
	}))
	if isErr {
		t.Fatalf("register with password failed: %s", text)
	}
	var reg2 struct {
		Address  string `json:"address"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(text), &reg2); err != nil {
		t.Fatalf("decode register result: %v", err)
	}
	if reg2.Address != "bob@test" || reg2.Password != "mypassword1" {
		t.Fatalf("explicit-password register = %+v", reg2)
	}
	regs = fs.requestsTo("/api/register")
	if len(regs) != 2 || regs[1].Body["password"] != "mypassword1" {
		t.Fatalf("explicit password not sent: %+v", regs)
	}

	text, isErr = toolText(t, callTool(t, s, "authenticate", map[string]any{"address": "a@test"}))
	if !isErr || !strings.Contains(text, "address and password are required") {
		t.Fatalf("missing password = %q, isError=%v", text, isErr)
	}

	code := authenticateTool(t, s, "  a@test  ", "pw")
	if code == "" {
		t.Fatal("trimmed authenticate returned no code")
	}
	verifies := fs.requestsTo("/api/verify-password")
	if len(verifies) == 0 || verifies[len(verifies)-1].Body["address"] != "a@test" {
		t.Fatalf("authenticate did not trim the address: %+v", verifies)
	}
}

func TestJSONRPCTransport(t *testing.T) {
	s := New("http://unused.example")
	var out bytes.Buffer
	s.out = &out

	call := func(line string) rpcResponse {
		t.Helper()
		out.Reset()
		s.handle(context.Background(), []byte(line))
		if out.Len() == 0 {
			return rpcResponse{}
		}
		var resp rpcResponse
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
			t.Fatalf("decode rpc response: %v (%s)", err, out.String())
		}
		return resp
	}

	resp := call(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if resp.Error != nil {
		t.Fatalf("ping error: %+v", resp.Error)
	}
	if _, ok := resp.Result.(map[string]any); !ok {
		t.Fatalf("ping result = %T", resp.Result)
	}

	resp = call(`{"jsonrpc":"2.0","id":2,"method":"nope"}`)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("unknown method = %+v", resp.Error)
	}

	resp = call(`{`)
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Fatalf("parse error = %+v", resp.Error)
	}

	out.Reset()
	s.handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if out.Len() != 0 {
		t.Fatalf("notification produced a response: %s", out.String())
	}

	resp = call(`{"jsonrpc":"2.0","id":3,"method":"initialize"}`)
	init, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("initialize result = %T", resp.Result)
	}
	if init["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocolVersion = %v", init["protocolVersion"])
	}
	info, _ := init["serverInfo"].(map[string]any)
	if info["name"] != "agentmail" {
		t.Fatalf("serverInfo = %+v", init["serverInfo"])
	}

	resp = call(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	list, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("tools/list result = %T", resp.Result)
	}
	tools, _ := list["tools"].([]any)
	if len(tools) != 14 {
		t.Fatalf("tools/list returned %d tools, want 14", len(tools))
	}
	names := map[string]bool{}
	for _, raw := range tools {
		if m, ok := raw.(map[string]any); ok {
			if name, _ := m["name"].(string); name != "" {
				names[name] = true
			}
		}
	}
	for _, want := range []string{"register", "authenticate", "send_email"} {
		if !names[want] {
			t.Fatalf("tools/list missing %q: %v", want, names)
		}
	}

	resp = call(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("unknown tool = %+v", resp.Error)
	}
}

func TestSendValidationDoesNotConsumeBudget(t *testing.T) {
	ts, _ := newFakeMailServer(t)
	s := newGatewayForTest(t, ts.URL)
	code := authenticateTool(t, s, "alice@test", "secret")

	cases := []map[string]any{
		{"access_code": code}, // no recipients
		{"access_code": code, "to": []string{"bob@test"}, "subject": "s"}, // no body
		{"access_code": code, "to": []string{"bob@test"}, "subject": "s", "body": "b",
			"inline_files": []string{"/definitely/missing.txt"}},
	}
	for i, args := range cases {
		text, isErr := toolText(t, callTool(t, s, "send_email", args))
		if !isErr {
			t.Fatalf("case %d: malformed send succeeded: %s", i, text)
		}
		if got := s.codes[code].CallsUsed; got != 0 {
			t.Fatalf("case %d: validation failure consumed %d budget slots", i, got)
		}
	}

	text, isErr := toolText(t, callTool(t, s, "send_email", map[string]any{
		"access_code": code, "to": []string{"bob@test"}, "subject": "s", "body": "b",
	}))
	if isErr {
		t.Fatalf("valid send failed: %s", text)
	}
	if got := s.codes[code].CallsUsed; got != 1 {
		t.Fatalf("valid send consumed %d slots, want 1", got)
	}
}

func TestReadsAndSubsListDoNotConsumeBudget(t *testing.T) {
	ts, _ := newFakeMailServer(t)
	s := newGatewayForTest(t, ts.URL)
	code := authenticateTool(t, s, "alice@test", "secret")

	for i := 0; i < 25; i++ {
		if text, isErr := toolText(t, callTool(t, s, "read_inbox", map[string]any{
			"access_code": code, "limit": 5,
		})); isErr {
			t.Fatalf("read %d failed: %s", i, text)
		}
	}
	for i := 0; i < 5; i++ {
		if text, isErr := toolText(t, callTool(t, s, "subs", map[string]any{
			"access_code": code, "action": "list",
		})); isErr {
			t.Fatalf("subs list %d failed: %s", i, text)
		}
	}
	if got := s.codes[code].CallsUsed; got != 0 {
		t.Fatalf("read-only tools consumed %d budget slots, want 0", got)
	}

	if text, isErr := toolText(t, callTool(t, s, "subs", map[string]any{
		"access_code": code, "action": "bogus",
	})); !isErr {
		t.Fatalf("invalid subs action succeeded: %s", text)
	}
	if got := s.codes[code].CallsUsed; got != 0 {
		t.Fatalf("invalid subs action consumed %d budget slots", got)
	}

	if text, isErr := toolText(t, callTool(t, s, "subs", map[string]any{
		"access_code": code, "action": "declare", "superior": "boss@test",
	})); isErr {
		t.Fatalf("subs declare failed: %s", text)
	}
	if got := s.codes[code].CallsUsed; got != 1 {
		t.Fatalf("subs declare consumed %d slots, want 1", got)
	}
}

func TestWriteBudgetExhaustionThroughTools(t *testing.T) {
	ts, _ := newFakeMailServer(t)
	s := newGatewayForTest(t, ts.URL)
	code := authenticateTool(t, s, "alice@test", "secret")

	for i := 0; i < CodeMaxCalls; i++ {
		if text, isErr := toolText(t, callTool(t, s, "send_email", map[string]any{
			"access_code": code, "to": []string{"bob@test"}, "subject": "s", "body": "b",
		})); isErr {
			t.Fatalf("send %d/%d failed early: %s", i+1, CodeMaxCalls, text)
		}
	}
	text, isErr := toolText(t, callTool(t, s, "send_email", map[string]any{
		"access_code": code, "to": []string{"bob@test"}, "subject": "s", "body": "b",
	}))
	if !isErr || !strings.Contains(text, "invalid or expired access code") {
		t.Fatalf("exhausted code = %q, isError=%v", text, isErr)
	}
}
