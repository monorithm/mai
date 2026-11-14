package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/procchain"
)

const testCursorKey = "mai1:cursor:testcursorsecret"

// guideOwnerFake is a fake server of mai for mai guide: the version needs no key, and the
// list of the agents needs the owner key. It records each request.
type guideOwnerFake struct {
	mu     sync.Mutex
	agents []string
	calls  []string
}

func (f *guideOwnerFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/version":
		_, _ = w.Write([]byte(`{"version":"3.2.0","contract":"3.2.0"}`))
	case r.Header.Get("Authorization") != "Bearer "+testOwnerKey:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_KEY","message":"bad key"}}`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/agents":
		var list []map[string]any
		for _, a := range f.agents {
			list = append(list, map[string]any{"name": a, "sessions": 0, "leases": 0})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"agents": list})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// guideTest is the scratch state of one test of mai guide: a temp HOME and MAI_HOME, a
// config.json with the port of a fake server, a key file, a fake engine, and a fake chain of
// parent processes with no app.
type guideTest struct {
	home   string
	port   int
	owner  *guideOwnerFake
	eng    *engineFake
	keys   *testKeyFile
	engOff bool
	// engHandler, when it is not nil, answers each request to the fake engine in place of eng.
	engHandler http.HandlerFunc
}

func newGuideTest(t *testing.T) *guideTest {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("MAI_HOME", filepath.Join(home, "maihome"))
	g := &guideTest{home: home, owner: &guideOwnerFake{}}
	s := httptest.NewServer(g.owner)
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	g.port, _ = strconv.Atoi(port)
	cfgDir := filepath.Join(home, "maihome", "config")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"instance":"mai","port":`+port+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g.keys = useTestKeys(t)
	fixedConnectBinary(t)
	g.eng = &engineFake{images: map[string]string{}}
	es := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.engHandler != nil {
			g.engHandler(w, r)
			return
		}
		g.eng.ServeHTTP(w, r)
	}))
	t.Cleanup(es.Close)
	oldFind, oldChain, oldLaunchers := lcFindEngine, guideChain, guideLaunchers
	lcFindEngine = func(context.Context) (*engine.Engine, error) {
		if g.engOff {
			return nil, engine.ErrNoResponse
		}
		api, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(es.URL, "http://")), client.WithAPIVersion("1.47"))
		if err != nil {
			return nil, err
		}
		return &engine.Engine{API: api}, nil
	}
	guideChain = func() ([]procchain.Proc, error) {
		return []procchain.Proc{{PID: 30, PPID: 20, Exe: "/opt/mai/bin/mai"}, {PID: 20, PPID: 1, Exe: "/bin/zsh"}}, nil
	}
	guideLaunchers = func(string) []engine.Launcher { return []engine.Launcher{{Name: "Docker Desktop"}} }
	t.Cleanup(func() { lcFindEngine, guideChain, guideLaunchers = oldFind, oldChain, oldLaunchers })
	return g
}

// serve makes the container of the instance run with the port of the fake server, and puts
// the owner key in the key file.
func (g *guideTest) serve(t *testing.T) {
	t.Helper()
	g.eng.ctrs = []fakeCtr{{ID: "c1", Name: "mai", Image: "sha256:img", Instance: "mai", Port: g.port, Running: true}}
	if err := g.keys.file.Set(keys.OwnerAccount("mai"), testOwnerKey); err != nil {
		t.Fatal(err)
	}
}

// addAgent makes the agent cursor exist on the fake server and in the key file.
func (g *guideTest) addAgent(t *testing.T) {
	t.Helper()
	g.owner.agents = []string{"cursor"}
	if err := g.keys.file.Set(keys.AgentAccount("mai", "cursor"), testCursorKey); err != nil {
		t.Fatal(err)
	}
}

func (g *guideTest) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runGuide(args, &out, &errOut)
	for _, secret := range []string{testOwnerKey, testCursorKey, "testownersecret", "testcursorsecret"} {
		if strings.Contains(out.String()+errOut.String(), secret) {
			t.Fatalf("mai guide %v shows a key:\n%s\n%s", args, out.String(), errOut.String())
		}
	}
	return code, out.String(), errOut.String()
}

// report runs mai guide --json and decodes the result.
func (g *guideTest) report(t *testing.T, args ...string) map[string]any {
	t.Helper()
	code, out, errOut := g.run(t, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("mai guide %v: exit %d: %s", args, code, errOut)
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("mai guide --json gives no JSON object: %v\n%s", err, out)
	}
	return r
}

func stepIDs(r map[string]any) []string {
	var ids []string
	for _, s := range r["steps"].([]any) {
		ids = append(ids, s.(map[string]any)["id"].(string))
	}
	return ids
}

func field(r map[string]any, path ...string) any {
	var v any = r
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func wantSteps(t *testing.T, r map[string]any, want ...string) {
	t.Helper()
	if got := stepIDs(r); !slices.Equal(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

func TestGuideWithTheEngineOffGivesTheStartFirst(t *testing.T) {
	g := newGuideTest(t)
	g.engOff = true
	r := g.report(t, "--app", "cursor")
	if field(r, "engine", "running") != false || field(r, "engine", "installed") != true {
		t.Fatalf("engine = %v", r["engine"])
	}
	if field(r, "container", "exists") != nil || field(r, "container", "reason") == nil {
		t.Fatalf("container = %v, want exists null with a reason", r["container"])
	}
	if field(r, "server", "running") != false {
		t.Fatalf("server = %v", r["server"])
	}
	wantSteps(t, r, "start", "add-agent", "show-change", "connect", "restart-app", "check")
	start := r["steps"].([]any)[0].(map[string]any)
	if start["command"] != "mai start" || start["ask_user"] != true || start["done"] != false ||
		!strings.Contains(start["changes"].(string), "550 MB") || !strings.Contains(start["changes"].(string), "engine") {
		t.Fatalf("start step = %v", start)
	}
	code, out, _ := g.run(t, "--app", "cursor")
	if code != 0 || !strings.Contains(out, "1. Start mai") || !strings.Contains(out, "Get the approval of the user first.") {
		t.Fatalf("text: exit %d\n%s", code, out)
	}
}

func TestGuideWithNoEngineGivesTheInstallStepOfTheUser(t *testing.T) {
	g := newGuideTest(t)
	g.engOff = true
	guideLaunchers = func(string) []engine.Launcher { return nil }
	r := g.report(t, "--app", "cursor")
	if field(r, "engine", "installed") != false {
		t.Fatalf("engine = %v", r["engine"])
	}
	steps := r["steps"].([]any)
	first := steps[0].(map[string]any)
	if first["id"] != "install-engine" || first["by"] != "user" || first["ask_user"] != true {
		t.Fatalf("first step = %v", first)
	}
}

func TestGuideWithAStoppedContainerGivesTheStart(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.eng.ctrs[0].Running = false
	r := g.report(t, "--app", "cursor")
	if field(r, "container", "exists") != true || field(r, "container", "running") != false || field(r, "container", "state") != "exited" {
		t.Fatalf("container = %v", r["container"])
	}
	if field(r, "server", "running") != false || field(r, "server", "version") != nil {
		t.Fatalf("server = %v", r["server"])
	}
	wantSteps(t, r, "start", "add-agent", "show-change", "connect", "restart-app", "check")
	if text := r["steps"].([]any)[0].(map[string]any)["text"].(string); !strings.Contains(text, "the container does not run") {
		t.Fatalf("start text = %q", text)
	}
	// mai guide sends no request to the port of a stopped container: a different program can use that port.
	if len(g.owner.calls) != 0 {
		t.Fatalf("calls to the port of a stopped container: %v", g.owner.calls)
	}
}

func TestGuideWithAServerThatRunsAndNoAgentGivesTheAgentStep(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	r := g.report(t, "--app", "cursor")
	if field(r, "server", "running") != true || field(r, "server", "version") != "3.2.0" || field(r, "server", "contract") != "3.2.0" {
		t.Fatalf("server = %v", r["server"])
	}
	if field(r, "owner_key", "exists") != true {
		t.Fatalf("owner key = %v", r["owner_key"])
	}
	if field(r, "agent", "name") != "cursor" || field(r, "agent", "exists") != false || field(r, "agent", "on_server") != false {
		t.Fatalf("agent = %v", r["agent"])
	}
	if field(r, "app", "connected") != false {
		t.Fatalf("app = %v", r["app"])
	}
	wantSteps(t, r, "add-agent", "show-change", "connect", "restart-app", "check")
	add := r["steps"].([]any)[0].(map[string]any)
	if add["command"] != "mai agent add cursor" || add["ask_user"] != true {
		t.Fatalf("add step = %v", add)
	}
	steps := r["steps"].([]any)
	if steps[1].(map[string]any)["command"] != "mai connect cursor --agent cursor --dry-run" ||
		steps[2].(map[string]any)["command"] != "mai connect cursor --agent cursor --yes" {
		t.Fatalf("connect steps = %v", steps[1:3])
	}
	if r := steps[3].(map[string]any); r["by"] != "user" || r["command"] != nil {
		t.Fatalf("restart step = %v", r)
	}
}

func TestGuideWithAnAgentAndNoConnectionGivesTheConnectSteps(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.addAgent(t)
	r := g.report(t, "--app", "cursor")
	if field(r, "agent", "exists") != true || field(r, "agent", "key") != true || field(r, "agent", "on_server") != true {
		t.Fatalf("agent = %v", r["agent"])
	}
	wantSteps(t, r, "show-change", "connect", "restart-app", "check")
}

func TestGuideWithAConnectedAppGivesOnlyTheCheck(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.addAgent(t)
	var out, errOut bytes.Buffer
	if code := runConnect([]string{"cursor", "--agent", "cursor", "--yes"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("connect: %d %s", code, errOut.String())
	}
	r := g.report(t, "--app", "cursor")
	if field(r, "app", "connected") != true {
		t.Fatalf("app = %v", r["app"])
	}
	wantSteps(t, r, "check")
	check := r["steps"].([]any)[0].(map[string]any)
	if check["tool"] != "whoami" || check["resource"] != "mai://guide" || check["ask_user"] != false ||
		!strings.Contains(check["text"].(string), "is connected") {
		t.Fatalf("check step = %v", check)
	}
	_, text, _ := g.run(t, "--app", "cursor")
	if !strings.Contains(text, "The app cursor is connected to mai as the agent cursor.") {
		t.Fatalf("text:\n%s", text)
	}
}

func TestGuideWithAnAgentOnTheServerAndNoKeyGivesTheRotateStep(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.owner.agents = []string{"cursor"}
	r := g.report(t, "--app", "cursor")
	if field(r, "agent", "exists") != false || field(r, "agent", "key") != false || field(r, "agent", "on_server") != true {
		t.Fatalf("agent = %v", r["agent"])
	}
	wantSteps(t, r, "rotate-agent-key", "show-change", "connect", "restart-app", "check")
}

func TestGuideWithAKeyOfTheAgentAndNoServerGivesNoAgentStep(t *testing.T) {
	g := newGuideTest(t)
	g.engOff = true
	if err := g.keys.file.Set(keys.AgentAccount("mai", "cursor"), testCursorKey); err != nil {
		t.Fatal(err)
	}
	r := g.report(t, "--app", "cursor")
	if field(r, "agent", "exists") != nil || field(r, "agent", "key") != true || field(r, "agent", "on_server") != nil ||
		field(r, "agent", "reason") == nil {
		t.Fatalf("agent = %v", r["agent"])
	}
	wantSteps(t, r, "start", "show-change", "connect", "restart-app", "check")
}

func TestGuideCountsAKeyInTheKeychainOfMai31(t *testing.T) {
	g := newGuideTest(t)
	g.engOff = true
	m := loadTestManifest(t, g.home)
	if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: keys.AgentAccount("mai", "cursor")}); err != nil {
		t.Fatal(err)
	}
	g.keys.legacy.Put(keys.AgentAccount("mai", "cursor"), testCursorKey)
	r := g.report(t, "--app", "cursor")
	if field(r, "agent", "key") != true || !strings.Contains(field(r, "agent", "reason").(string), "keychain") {
		t.Fatalf("agent = %v", r["agent"])
	}
	// mai guide reads no keychain item, thus the item stays.
	if len(g.keys.legacy.Accounts()) != 1 {
		t.Fatalf("the keychain items changed: %v", g.keys.legacy.Accounts())
	}
	// The key is in the keychain, thus the text does not tell that the key file has it.
	_, out, _ := g.run(t, "--app", "cursor")
	if strings.Contains(out, "The key file has the key of the agent") {
		t.Fatalf("the text tells that the key file has the key of the agent:\n%s", out)
	}
}

func TestGuideFindsTheAppInTheChainOfParentProcesses(t *testing.T) {
	g := newGuideTest(t)
	guideChain = func() ([]procchain.Proc, error) {
		return []procchain.Proc{
			{PID: 40, PPID: 30, Exe: "/opt/mai/bin/mai"},
			{PID: 30, PPID: 20, Exe: "/bin/zsh"},
			{PID: 20, PPID: 10, Exe: "/Applications/Cursor.app/Contents/Frameworks/Cursor Helper (Plugin).app/Contents/MacOS/Cursor Helper (Plugin)"},
			{PID: 10, PPID: 1, Exe: "/Applications/Cursor.app/Contents/MacOS/Cursor"},
			{PID: 1, PPID: 0, Exe: "/sbin/launchd"},
		}, nil
	}
	r := g.report(t)
	if field(r, "app", "name") != "cursor" || field(r, "app", "found_by") != "process" || field(r, "agent", "name") != "cursor" {
		t.Fatalf("app = %v, agent = %v", r["app"], r["agent"])
	}
	_, text, _ := g.run(t)
	if !strings.Contains(text, "chain of parent processes") || !strings.Contains(text, "mai guide --app APP") {
		t.Fatalf("text:\n%s", text)
	}
}

func TestGuideWithNoAppInTheChainTellsTheAgentToGiveTheApp(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	r := g.report(t)
	if field(r, "app", "name") != nil || field(r, "app", "found_by") != nil || field(r, "app", "reason") == nil {
		t.Fatalf("app = %v", r["app"])
	}
	wantSteps(t, r, "select-app")
	step := r["steps"].([]any)[0].(map[string]any)
	if step["command"] != "mai guide --app APP" || !strings.Contains(step["text"].(string), "antigravity, claude, codex, copilot, cursor") {
		t.Fatalf("step = %v", step)
	}
	guideChain = func() ([]procchain.Proc, error) { return nil, errors.New("no process table") }
	r = g.report(t)
	if field(r, "app", "name") != nil || !strings.Contains(field(r, "app", "reason").(string), "no process table") {
		t.Fatalf("app = %v", r["app"])
	}
}

func TestGuideWithTheFlagAppAndTheFlagAgent(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	r := g.report(t, "--app", "codex", "--agent", "lane")
	if field(r, "app", "name") != "codex" || field(r, "app", "found_by") != "flag" || field(r, "app", "title") != "Codex" {
		t.Fatalf("app = %v", r["app"])
	}
	if field(r, "agent", "name") != "lane" {
		t.Fatalf("agent = %v", r["agent"])
	}
	steps := r["steps"].([]any)
	if steps[0].(map[string]any)["command"] != "mai agent add lane" ||
		steps[2].(map[string]any)["command"] != "mai connect codex --agent lane --yes" {
		t.Fatalf("steps = %v", steps)
	}
	if field(r, "lanes", "command") != "mai session new lane --label LABEL" {
		t.Fatalf("lanes = %v", r["lanes"])
	}
}

func TestGuideRefusesAnUnknownAppAndABadAgentName(t *testing.T) {
	g := newGuideTest(t)
	for _, args := range [][]string{{"--app", "vim"}, {"--app", "vim", "--json"}} {
		code, out, errOut := g.run(t, args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "antigravity, claude, codex, copilot, cursor") ||
			!strings.Contains(errOut, guideUse) {
			t.Fatalf("mai guide %v: exit %d\nout: %s\nerr: %s", args, code, out, errOut)
		}
	}
	for _, args := range [][]string{{"--agent", "Bad.Name"}, {"extra"}, {"--nope"}} {
		if code, _, _ := g.run(t, args...); code != 2 {
			t.Fatalf("mai guide %v: exit %d, want 2", args, code)
		}
	}
}

func TestGuideJSONHasTheStableFields(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	r := g.report(t, "--app", "cursor")
	if r["for"] != "ai-agent" || r["cli_version"] == nil || r["instance"] != "mai" {
		t.Fatalf("report = %v", r)
	}
	want := map[string][]string{
		"":          {"for", "about", "cli_version", "instance", "reason", "engine", "container", "server", "owner_key", "app", "agent", "steps", "rerun", "lanes", "rules"},
		"engine":    {"running", "installed", "name", "reason"},
		"container": {"name", "exists", "running", "state", "image", "image_local", "port", "reason"},
		"server":    {"url", "running", "version", "contract", "reason"},
		"owner_key": {"exists", "accepted", "reason"},
		"app":       {"name", "title", "found_by", "connected", "reason", "apps"},
		"agent":     {"name", "exists", "key", "on_server", "reason"},
		"lanes":     {"text", "command", "example", "argument", "login_call", "ask_user"},
	}
	for obj, keys := range want {
		m := r
		if obj != "" {
			m = r[obj].(map[string]any)
		}
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				t.Errorf("the object %q has no field %q: %v", obj, k, m)
			}
		}
		if len(m) != len(keys) {
			t.Errorf("the object %q has %d fields, want %d: %v", obj, len(m), len(keys), m)
		}
	}
	for _, s := range r["steps"].([]any) {
		step := s.(map[string]any)
		for _, k := range []string{"id", "text", "by", "command", "tool", "resource", "changes", "ask_user", "done"} {
			if _, ok := step[k]; !ok {
				t.Errorf("the step %v has no field %q", step["id"], k)
			}
		}
		if step["done"] != false {
			t.Errorf("the step %v is done", step["id"])
		}
	}
	if len(r["rules"].([]any)) != len(guideRules) || field(r, "lanes", "login_call") != false {
		t.Fatalf("rules = %v, lanes = %v", r["rules"], r["lanes"])
	}
}

func TestGuideChangesNothing(t *testing.T) {
	g := newGuideTest(t)
	// With no state folder, mai guide makes no folder and no key file.
	before := treeState(t, g.home)
	g.engOff = true
	g.run(t, "--app", "cursor")
	g.run(t, "--app", "claude", "--json")
	sameTree(t, before, treeState(t, g.home))

	g.engOff = false
	g.serve(t)
	g.addAgent(t)
	before = treeState(t, g.home)
	for _, app := range []string{"claude", "cursor", "copilot", "codex", "antigravity"} {
		g.run(t, "--app", app)
		g.run(t, "--app", app, "--json")
	}
	sameTree(t, before, treeState(t, g.home))
	for _, c := range g.eng.calls {
		if !strings.HasPrefix(c, "GET ") && !strings.HasPrefix(c, "HEAD ") {
			t.Errorf("mai guide sent a request that can change the engine: %s", c)
		}
	}
	for _, c := range g.owner.calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Errorf("mai guide sent a request that can change the server: %s", c)
		}
	}
	if len(g.owner.calls) == 0 {
		t.Fatal("mai guide sent no request to the server")
	}
}

func TestGuideTextHasEachPartInEachState(t *testing.T) {
	g := newGuideTest(t)
	g.engOff = true
	_, off, _ := g.run(t, "--app", "cursor")
	g.engOff = false
	g.serve(t)
	_, up, _ := g.run(t, "--app", "cursor")
	g.addAgent(t)
	_, added, _ := g.run(t, "--app", "cursor")
	for name, text := range map[string]string{"engine off": off, "server up": up, "agent added": added} {
		for _, part := range []string{"This text is for an AI agent.", "State:", "Next steps:", "Named sessions (lanes):", "Rules:",
			"mai session new cursor --label lane-1", "session: \"lane-1\"", "keys.json"} {
			if !strings.Contains(text, part) {
				t.Errorf("%s: the text has no %q:\n%s", name, part, text)
			}
		}
	}
	if !strings.Contains(off, "the engine does not run") || strings.Contains(up, "Start mai") || strings.Contains(added, "mai agent add") {
		t.Fatalf("texts:\n%s\n---\n%s\n---\n%s", off, up, added)
	}
}

func TestGuideAppOfAProcess(t *testing.T) {
	cases := map[string]string{
		"/Users/a/.local/bin/claude":                 "claude",
		"/home/a/.local/share/claude/versions/2.1.0": "claude",
		"/Users/a/Library/Application Support/Claude/claude-code/2.1.0/x/claude.app/Contents/MacOS/claude":                      "claude",
		"/Applications/Cursor.app/Contents/MacOS/Cursor":                                                                        "cursor",
		`C:\Users\a\AppData\Local\Programs\cursor\Cursor.exe`:                                                                   "cursor",
		"/usr/local/bin/cursor-agent":                                                                                           "cursor",
		"/Applications/Visual Studio Code.app/Contents/MacOS/Electron":                                                          "copilot",
		"/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper (Plugin).app/Contents/MacOS/Code Helper (Plugin)": "copilot",
		`C:\Users\a\AppData\Local\Programs\Microsoft VS Code\Code.exe`:                                                          "copilot",
		"/usr/share/code/code": "copilot",
		"/opt/homebrew/lib/node_modules/@openai/codex/vendor/aarch64-apple-darwin/codex/codex": "codex",
		"/Applications/Codex.app/Contents/MacOS/Codex":                                         "codex",
		"/Applications/Antigravity.app/Contents/MacOS/Electron":                                "antigravity",
		"/usr/bin/antigravity":                           "antigravity",
		"/Applications/Claude.app/Contents/MacOS/Claude": "",
		"/bin/zsh": "",
		"/System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal": "",
		"/usr/local/bin/node": "",
	}
	for exe, want := range cases {
		if got := guideAppOfProc(procchain.Proc{Exe: exe}); got != want {
			t.Errorf("app of %s = %q, want %q", exe, got, want)
		}
	}
	// The nearest app is the app of the agent, also in a terminal of a different app.
	chain := []procchain.Proc{
		{PID: 50, Exe: "/Users/a/.local/bin/claude"},
		{PID: 40, Exe: "/bin/zsh"},
		{PID: 30, Exe: "/Users/a/.local/bin/claude"},
		{PID: 20, Exe: "/Applications/Cursor.app/Contents/MacOS/Cursor"},
	}
	if got := guideAppOf(chain); got != "claude" {
		t.Fatalf("app of the chain = %q, want claude", got)
	}
	// The first process of the chain is mai itself, thus its name does not count.
	if got := guideAppOf(chain[:1]); got != "" {
		t.Fatalf("app of a chain with only this process = %q", got)
	}
}

func TestTheUsageStartsWithTheLineForAnAgent(t *testing.T) {
	var b bytes.Buffer
	writeUsage(&b)
	first, rest, _ := strings.Cut(b.String(), "\n")
	if first != "An AI agent: run mai guide." {
		t.Fatalf("first line of the usage = %q", first)
	}
	if !strings.Contains(rest, "  guide ") {
		t.Fatalf("the usage has no command guide:\n%s", b.String())
	}
}
