package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// The server command "mai owner set-key" writes this line to stderr when the server has an
// owner key.
const serverKeyExistsLine = "mai: an owner key exists. Use --replace to change it.\n"

// startOfAnExistingContainer runs mai start for the running container of the instance
// mai-wf-h. The server has an owner key, and "mai owner set-key" writes text to stderr.
func startOfAnExistingContainer(t *testing.T, ownerKey bool, code int, stderr string) (int, string) {
	t.Helper()
	t.Setenv("MAI_HOME", t.TempDir())
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(`{"instance":"mai-wf-h","port":18995}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k := useTestKeys(t)
	if ownerKey {
		if err := k.file.Set(keys.OwnerAccount("mai-wf-h"), testOwnerKey); err != nil {
			t.Fatal(err)
		}
	}
	f := &execFake{engineFake: &engineFake{ctrs: []fakeCtr{
		{ID: "ch", Name: "mai-wf-h", Image: "sha256:img", Instance: "mai-wf-h", Port: 18995, Running: true},
	}}, execs: map[string][]string{}, stdin: map[string]bool{},
		codes:  map[string]int{"mai owner set-key": code},
		stderr: map[string]string{"mai owner set-key": stderr}}
	useExecFake(t, f)
	out, errs := captureLifecycle(t)
	return runStart([]string{"--no-pull"}), out.String() + errs.String()
}

// A start of a container with an owner key is a normal restart. Then the server tells that
// an owner key exists, and that --replace changes it. The command mai start does not show
// that text. An agent can do that step, and the step replaces the owner key of the server.
func TestStartOfAContainerWithAnOwnerKeyShowsNoReplaceStep(t *testing.T) {
	code, out := startOfAnExistingContainer(t, true, lcOwnerKeyExists, serverKeyExistsLine)
	if code != 0 {
		t.Fatalf("mai start: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "--replace") || strings.Contains(out, "owner key exists") {
		t.Fatalf("a normal restart shows a step with --replace:\n%s", out)
	}
}

// When the key file had no owner key, mai start makes one, and the server refuses it. Then
// the CLI tells it with its own text, and not with the text of the server.
func TestStartWithANewOwnerKeyThatTheServerRefusesTellsIt(t *testing.T) {
	code, out := startOfAnExistingContainer(t, false, lcOwnerKeyExists, serverKeyExistsLine)
	if code != 0 {
		t.Fatalf("mai start: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "the server has a different owner key") || strings.Contains(out, "owner key exists") {
		t.Fatalf("output:\n%s", out)
	}
}

// An error of "mai owner set-key" stays visible.
func TestStartShowsTheErrorTextOfSetKey(t *testing.T) {
	code, out := startOfAnExistingContainer(t, true, 1, "mai: the database is not ready\n")
	if code == 0 || !strings.Contains(out, "the database is not ready") {
		t.Fatalf("mai start: exit %d\n%s", code, out)
	}
}

// The rules of the guide name the commands that replace the owner key of the server. The step
// start tells the agent not to do a step with --replace from the output of mai start.
func TestGuideTellsTheAgentNotToReplaceTheOwnerKey(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.eng.ctrs[0].Running = false
	r := g.report(t, "--app", "cursor")
	rules := strings.Join(func() []string {
		var out []string
		for _, rule := range r["rules"].([]any) {
			out = append(out, rule.(string))
		}
		return out
	}(), "\n")
	for _, cmd := range []string{"`mai exec`", "`mai owner set-key --replace`"} {
		if !strings.Contains(rules, cmd) {
			t.Errorf("the rules do not name %s:\n%s", cmd, rules)
		}
	}
	start := r["steps"].([]any)[0].(map[string]any)
	if start["id"] != "start" || !strings.Contains(start["changes"].(string), "`--replace`") ||
		!strings.Contains(start["changes"].(string), "do not do that step") {
		t.Fatalf("start step = %v", start)
	}
}
