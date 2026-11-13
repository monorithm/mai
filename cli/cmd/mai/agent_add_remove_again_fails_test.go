package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// The write of the new agent key to the key file fails, and the remove of the new agent on
// the server also fails. The agent then exists on the server with a key that no file holds.
// The command must not tell that the agent is removed again.
func TestAgentAddDoesNotTellARemoveThatFailed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MAI_HOME", filepath.Join(home, "maihome"))
	k := useTestKeys(t)
	if err := k.file.Set(keys.OwnerAccount("mai"), testOwnerKey); err != nil {
		t.Fatal(err)
	}
	removed := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents":
			// The key file becomes a file that is not valid JSON, thus the write fails. A
			// file mode does not make the write fail on Windows.
			_ = os.WriteFile(k.file.Path(), []byte("{"), 0o600)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "cursor", "agent_key": "mai1:cursor:newsecret"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/agents/cursor":
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL","message":"the remove failed"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	cfgDir := filepath.Join(home, "maihome", "config")
	_ = os.MkdirAll(cfgDir, 0o700)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"instance":"mai","port":`+port+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runAgent([]string{"add", "cursor"}, &out, &errOut)
	if code == 0 {
		t.Fatal("mai agent add gave exit 0")
	}
	noSecret(t, "mai agent add", out.String()+errOut.String())
	if !removed && strings.Contains(errOut.String(), "is removed again") {
		t.Fatalf("the remove of the agent on the server failed, but the command tells that it is removed: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "stays on the server") || !strings.Contains(errOut.String(), "mai agent rotate cursor") {
		t.Errorf("the message does not tell that the agent stays, or gives no step: %q", errOut.String())
	}
}
