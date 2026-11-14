package main

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// guideStatePart gives the lines of the part State of the text of mai guide.
func guideStatePart(t *testing.T, text string) string {
	t.Helper()
	_, rest, ok := strings.Cut(text, "\nState:\n")
	if !ok {
		t.Fatalf("the text has no part State:\n%s", text)
	}
	state, _, ok := strings.Cut(rest, "\nNext steps:\n")
	if !ok {
		t.Fatalf("the text has no part Next steps:\n%s", text)
	}
	return state
}

// guideStepBlocks gives the text of each step of the text of mai guide, in sequence.
func guideStepBlocks(t *testing.T, text string) []string {
	t.Helper()
	_, rest, ok := strings.Cut(text, "\nNext steps:\n")
	if !ok {
		t.Fatalf("the text has no part Next steps:\n%s", text)
	}
	rest, _, _ = strings.Cut(rest, "\nNamed sessions (lanes):\n")
	var blocks []string
	for _, b := range strings.Split(rest, "\n\n") {
		b = strings.TrimSpace(b)
		if b != "" && b[0] >= '1' && b[0] <= '9' {
			blocks = append(blocks, b)
		}
	}
	return blocks
}

// wantNoStepThatFails makes sure that no step gives a command that fails in this state.
func wantNoStepThatFails(t *testing.T, r map[string]any, commands ...string) {
	t.Helper()
	for _, s := range r["steps"].([]any) {
		step := s.(map[string]any)
		cmd, _ := step["command"].(string)
		for _, bad := range commands {
			if strings.HasPrefix(cmd, bad) {
				t.Errorf("the step %v gives %q, and that command fails in this state: %v", step["id"], cmd, step)
			}
		}
	}
}

// A container of a 2.x install holds the name of the instance. mai start refuses it, thus the
// guide gives no start, and only a step that names the choices of the user.
func TestGuideForAContainerOfA2xInstallGivesTheChoicesOfTheUser(t *testing.T) {
	g := newGuideTest(t)
	g.eng.ctrs = []fakeCtr{{ID: "c1", Name: "mai", Image: "sha256:img", Port: g.port, Running: true,
		Labels: map[string]string{"com.docker.compose.project": "mai"}}}
	r := g.report(t, "--app", "cursor")
	wantSteps(t, r, "legacy-container")
	wantNoStepThatFails(t, r, "mai start", "mai agent add", "mai connect")
	step := r["steps"].([]any)[0].(map[string]any)
	text := step["text"].(string)
	if step["by"] != "user" || step["ask_user"] != true || step["command"] != "mai uninstall --legacy" ||
		!strings.Contains(text, "`mai uninstall --legacy`") || !strings.Contains(text, "--instance NAME") ||
		!strings.Contains(text, "only when the user tells you to do it") || !strings.Contains(text, "stop") {
		t.Fatalf("step = %v", step)
	}
	if field(r, "container", "exists") != true || field(r, "server", "running") != false {
		t.Fatalf("container = %v, server = %v", r["container"], r["server"])
	}
	_, out, _ := g.run(t, "--app", "cursor")
	if strings.Contains(out, "..") {
		t.Errorf("the text has \"..\":\n%s", out)
	}
	state := guideStatePart(t, out)
	for _, order := range []string{"Run ", "mai uninstall", "use --instance"} {
		if strings.Contains(state, order) {
			t.Errorf("the part State has the order %q:\n%s", order, state)
		}
	}
	blocks := guideStepBlocks(t, out)
	if len(blocks) != 1 || !strings.Contains(blocks[0], "Get the approval of the user first.") {
		t.Fatalf("the step has no approval text:\n%s", out)
	}
}

// A container that mai 3 did not make, and that is not of a 2.x install, holds the name of
// the instance. mai start refuses it, and mai uninstall --legacy does not remove it.
func TestGuideForADifferentContainerWithTheNameGivesNoStart(t *testing.T) {
	g := newGuideTest(t)
	g.eng.ctrs = []fakeCtr{{ID: "c1", Name: "mai", Image: "sha256:img", Port: g.port, Running: false}}
	r := g.report(t, "--app", "cursor")
	wantSteps(t, r, "legacy-container")
	wantNoStepThatFails(t, r, "mai start", "mai agent add", "mai connect", "mai uninstall")
	step := r["steps"].([]any)[0].(map[string]any)
	if step["by"] != "user" || step["command"] != nil || !strings.Contains(step["text"].(string), "--instance NAME") {
		t.Fatalf("step = %v", step)
	}
	_, out, _ := g.run(t, "--app", "cursor")
	if strings.Contains(out, "..") || strings.Contains(guideStatePart(t, out), "Run ") {
		t.Errorf("text:\n%s", out)
	}
}

// A config.json that the CLI cannot read stops mai start, mai agent add, and mai connect.
// Thus the guide gives only a step of the user.
func TestGuideWithAConfigThatCannotBeReadGivesOnlyAStepOfTheUser(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	if err := os.WriteFile(filepath.Join(g.home, "maihome", "config", "config.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := g.report(t, "--app", "codex")
	wantSteps(t, r, "config-error")
	step := r["steps"].([]any)[0].(map[string]any)
	if step["by"] != "user" || step["command"] != nil || !strings.Contains(step["text"].(string), "config.json") ||
		!strings.Contains(step["text"].(string), "stop") {
		t.Fatalf("step = %v", step)
	}
	for _, s := range r["steps"].([]any) {
		if text := s.(map[string]any)["text"].(string); strings.Contains(text, "the container does not exist") {
			t.Errorf("the step %v tells that the container does not exist: %q", s.(map[string]any)["id"], text)
		}
	}
	if r["instance"] != nil || r["reason"] == nil || field(r, "container", "exists") != nil {
		t.Fatalf("instance = %v, reason = %v, container = %v", r["instance"], r["reason"], r["container"])
	}
}

// A key file that the CLI cannot read stops mai start and mai agent add. Thus the guide
// gives only a step of the user.
func TestGuideWithAKeyFileThatCannotBeReadGivesNoStartAndNoAdd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the CLI examines the mode of the key file only on Unix")
	}
	for _, running := range []bool{true, false} {
		g := newGuideTest(t)
		g.serve(t)
		g.eng.ctrs[0].Running = running
		if err := g.keys.file.Set(keys.AgentAccount("mai", "other"), "mai1:other:x"); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(g.keys.file.Path(), 0o644); err != nil {
			t.Fatal(err)
		}
		r := g.report(t, "--app", "cursor")
		wantSteps(t, r, "key-file-error")
		wantNoStepThatFails(t, r, "mai start", "mai agent add")
		step := r["steps"].([]any)[0].(map[string]any)
		if step["by"] != "user" || !strings.Contains(step["text"].(string), "key file") {
			t.Fatalf("running %v: step = %v", running, step)
		}
		// The error of the key file gives a command. The part State quotes it as data, thus
		// it is not an order of the guide.
		_, out, _ := g.run(t, "--app", "cursor")
		state := regexp.MustCompile("`[^`]*`").ReplaceAllString(guideStatePart(t, out), "CODE")
		if strings.Contains(state, "Run") || strings.Contains(state, "chmod") {
			t.Errorf("running %v: the part State has an order out of a code span:\n%s", running, state)
		}
	}
}

// When the engine runs, but the CLI cannot read the container, the container can exist. Thus
// the guide does not tell that the container does not exist, and gives no start.
func TestGuideWithAContainerThatCannotBeReadGivesNoStart(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.engHandler = func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"test: the engine cannot read the container"}`))
			return
		}
		g.eng.ServeHTTP(w, r)
	}
	r := g.report(t, "--app", "cursor")
	wantSteps(t, r, "container-error")
	wantNoStepThatFails(t, r, "mai start", "mai agent add", "mai connect")
	if field(r, "container", "exists") != nil || !strings.Contains(field(r, "container", "reason").(string), "cannot read the container") {
		t.Fatalf("container = %v", r["container"])
	}
}
