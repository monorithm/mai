package bridge

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// A new agent key of "mai agent rotate" applies at the next lease request of a bridge that
// runs, with no restart of the bridge.
func TestARotatedAgentKeyAppliesAtTheNextLeaseRequest(t *testing.T) {
	const rotated = "mai1:cursor:rotatedkeyrotatedkeyrotatedkey"
	var mu sync.Mutex
	var bearers []string
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/leases" {
				b := bearer(r)
				mu.Lock()
				bearers = append(bearers, b)
				mu.Unlock()
				// The fake server knows one key. It accepts the new key in its place.
				if b == rotated {
					r.Header.Set("Authorization", "Bearer "+agentKey)
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	h.recv(t)
	h.keys.set(keys.AgentAccount("mai", "cursor"), rotated)
	h.login(t, 2, "lane-a")
	h.stop(t)
	mu.Lock()
	defer mu.Unlock()
	if len(bearers) != 2 || bearers[0] != agentKey || bearers[1] != rotated {
		t.Fatalf("the lease requests used %d keys; the second key is the new key: %v", len(bearers), len(bearers) == 2 && bearers[1] == rotated)
	}
}

// When the key file has no key for the agent at a login, the login gives BAD_KEY with the
// command that adds the agent.
func TestALoginWithNoAgentKeyGivesBadKey(t *testing.T) {
	h := startNamed(t)
	h.keys.set(keys.AgentAccount("mai", "cursor"), "")
	r := result(t, h.call(t, 1, "login", map[string]any{"session": "lane-a"}))
	sc, _ := r["structuredContent"].(map[string]any)
	e, _ := sc["error"].(map[string]any)
	if r["isError"] != true || e["code"] != "BAD_KEY" || !strings.Contains(e["message"].(string), "mai agent add cursor") {
		t.Fatalf("login with no key = %v", r)
	}
	h.stop(t)
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.leaseReqs) != 1 {
		t.Fatalf("the bridge sent a lease request with no key: %v", h.srv.leaseReqs)
	}
}

// The bridge reads the agent key from the key file of the CLI.
func TestTheBridgeReadsTheKeyFileOfTheCLI(t *testing.T) {
	f := keys.Open(t.TempDir(), nil)
	if err := f.Set(keys.AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, newTestKeys())
	h.b.Keys = f
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami"}}`)
	if a := h.recv(t); result(t, a)["session"] != "cursor.s1" {
		t.Fatalf("whoami = %v", a)
	}
	h.stop(t)
}

// A bridge with no key source does not send a lease request.
func TestABridgeWithNoKeySourceStops(t *testing.T) {
	h := newHarness(t, newTestKeys())
	h.b.Keys = nil
	h.start(context.Background())
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if len(h.srv.leaseReqs) != 0 {
		t.Fatal("the bridge sent a lease request")
	}
}
