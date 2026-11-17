package gateway

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/lieuyimo/mailofagents/internal/audit"
	"github.com/lieuyimo/mailofagents/internal/config"
	"github.com/lieuyimo/mailofagents/internal/server"
	"github.com/lieuyimo/mailofagents/internal/store"
)

// TestRegisterAgainstRealServer is the seam the fake-server tests missed:
// the gateway used to send only a name while the real server requires a
// password, so MCP register always returned 403. A fake server that ignores
// the contract cannot catch that.
func TestRegisterAgainstRealServer(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"), store.WithBcryptCost(bcrypt.MinCost))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.DB().Close() })
	if err := st.BootstrapSystem("admin", "adminpassword1", "test.example"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	a, err := audit.New(st.DB())
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	ts := httptest.NewServer(server.New(st, a, &config.Config{}).Handler())
	t.Cleanup(ts.Close)

	s := New(ts.URL)
	s.out = io.Discard

	text, isErr := toolText(t, callTool(t, s, "register", map[string]any{"name": "agent-one"}))
	if isErr {
		t.Fatalf("register through the real server failed: %s", text)
	}
	var reg struct {
		Address  string `json:"address"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(text), &reg); err != nil {
		t.Fatalf("decode register result: %v", err)
	}
	if reg.Address == "" || len(reg.Password) < 8 {
		t.Fatalf("register result = %+v", reg)
	}

	code := authenticateTool(t, s, reg.Address, reg.Password)
	if text, isErr := toolText(t, callTool(t, s, "read_inbox", map[string]any{
		"access_code": code, "limit": 5,
	})); isErr {
		t.Fatalf("read_inbox with the registered credentials failed: %s", text)
	}
}
