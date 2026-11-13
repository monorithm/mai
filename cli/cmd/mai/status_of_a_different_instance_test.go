package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// fakeDocker is the part of the Engine API that "mai status" uses. It has one running
// container of mai-wf-a, with the host port 18990.
func fakeDocker(t *testing.T) *engine.Engine {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/_ping"):
			w.Header().Set("API-Version", "1.47")
			_, _ = io.WriteString(w, "OK")
		case strings.HasSuffix(p, "/version"):
			_, _ = io.WriteString(w, `{"Version":"29.0.0","ApiVersion":"1.47"}`)
		case strings.HasSuffix(p, "/containers/json"):
			_, _ = io.WriteString(w, `[{"Id":"ca","Names":["/`+instA+`"]}]`)
		case strings.HasSuffix(p, "/containers/ca/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": "ca", "Name": "/" + instA, "Image": "sha256:img",
				"State":      map[string]any{"Status": "running", "Running": true},
				"Config":     map[string]any{"Image": "mai:test", "Labels": engine.Labels(instA)},
				"HostConfig": map[string]any{"PortBindings": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "18990"}}}},
			})
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"test: not in the fake"}`)
		}
	}))
	t.Cleanup(s.Close)
	api, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(s.URL, "http://")), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	return &engine.Engine{API: api}
}

// "mai status --instance mai-wf-a" must show the port of mai-wf-a, and must not send the owner
// key of mai-wf-a to the server on the port of config.json, which is the server of mai-wf-b.
func TestStatusOfADifferentInstanceUsesNoPortOfTheInstanceOfConfig(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+" "+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/version" {
			_, _ = io.WriteString(w, `{"version":"9.9.9-of-b","contract":"3.2.0"}`)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"BAD_KEY","message":"bad key"}}`)
	}))
	t.Cleanup(srv.Close)
	_, portB, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(`{"instance":"`+instB+`","port":`+portB+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k := useTestKeys(t)
	if err := k.file.Set(keys.OwnerAccount(instA), fileKeyOfA); err != nil {
		t.Fatal(err)
	}
	e := fakeDocker(t)
	old := lcConnectEngine
	lcConnectEngine = func(context.Context, io.Writer) (*engine.Engine, error) { return e, nil }
	t.Cleanup(func() { lcConnectEngine = old })
	out, _ := captureLifecycle(t)
	_ = runStatus([]string{"--instance", instA})
	text := out.String()
	if !strings.Contains(text, "127.0.0.1:18990") {
		t.Errorf("mai status --instance %s does not show the port 18990 of its container:\n%s", instA, text)
	}
	if strings.Contains(text, "9.9.9-of-b") {
		t.Errorf("mai status --instance %s shows the version of the server of %s:\n%s", instA, instB, text)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if strings.HasSuffix(s, " "+fileKeyOfA) {
			t.Errorf("mai status --instance %s sent the owner key of %s to the server of %s: %s", instA, instA, instB, strings.TrimSuffix(s, " "+fileKeyOfA))
		}
	}
}
