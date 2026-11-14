package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// guideStates sets up many states of mai for the tests of the text of mai guide. Each state
// gives the arguments of the run.
var guideStates = map[string]func(t *testing.T, g *guideTest) []string{
	"engine off": func(t *testing.T, g *guideTest) []string { g.engOff = true; return []string{"--app", "cursor"} },
	"no engine": func(t *testing.T, g *guideTest) []string {
		g.engOff = true
		guideLaunchers = func(string) []engine.Launcher { return nil }
		return []string{"--app", "cursor"}
	},
	"engine off with an agent key": func(t *testing.T, g *guideTest) []string {
		g.engOff = true
		setKey(t, g, keys.AgentAccount("mai", "cursor"), testCursorKey)
		return []string{"--app", "cursor"}
	},
	"no container": func(t *testing.T, g *guideTest) []string { return []string{"--app", "cursor"} },
	"stopped": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		g.eng.ctrs[0].Running = false
		return []string{"--app", "cursor"}
	},
	"stopped with an agent": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		g.addAgent(t)
		g.eng.ctrs[0].Running = false
		return []string{"--app", "cursor"}
	},
	"stopped with no owner key": func(t *testing.T, g *guideTest) []string {
		g.eng.ctrs = []fakeCtr{{ID: "c1", Name: "mai", Image: "sha256:img", Instance: "mai", Port: g.port}}
		return []string{"--app", "cursor"}
	},
	"running": func(t *testing.T, g *guideTest) []string { g.serve(t); return []string{"--app", "cursor"} },
	"running with agent": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		g.addAgent(t)
		return []string{"--app", "cursor"}
	},
	"agent with no key": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		g.owner.agents = []string{"cursor"}
		return []string{"--app", "cursor"}
	},
	"agent key with no account": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		setKey(t, g, keys.AgentAccount("mai", "cursor"), testCursorKey)
		return []string{"--app", "cursor"}
	},
	"no owner key": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		if err := g.keys.file.Delete(keys.OwnerAccount("mai")); err != nil {
			t.Fatal(err)
		}
		return []string{"--app", "cursor"}
	},
	"refused owner key": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		setKey(t, g, keys.OwnerAccount("mai"), "mai1:owner:refusedsecret")
		return []string{"--app", "cursor"}
	},
	"keys of mai 3.1": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		if err := g.keys.file.Delete(keys.OwnerAccount("mai")); err != nil {
			t.Fatal(err)
		}
		m := loadTestManifest(t, g.home)
		for _, a := range []string{keys.OwnerAccount("mai"), keys.AgentAccount("mai", "cursor")} {
			if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: a}); err != nil {
				t.Fatal(err)
			}
			g.keys.legacy.Put(a, "mai1:legacy:x")
		}
		return []string{"--app", "cursor"}
	},
	"port of config.json": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		writeConfig(t, g, `{"instance":"mai","port":1}`)
		return []string{"--app", "cursor"}
	},
	"container of a 2.x install": func(t *testing.T, g *guideTest) []string {
		g.eng.ctrs = []fakeCtr{{ID: "c1", Name: "mai", Image: "sha256:img", Port: g.port, Running: true,
			Labels: map[string]string{"com.docker.compose.project": "mai"}}}
		return []string{"--app", "cursor"}
	},
	"different container": func(t *testing.T, g *guideTest) []string {
		g.eng.ctrs = []fakeCtr{{ID: "c1", Name: "mai", Image: "sha256:img", Port: g.port}}
		return []string{"--app", "cursor"}
	},
	"bad config.json": func(t *testing.T, g *guideTest) []string {
		writeConfig(t, g, "{bad")
		return []string{"--app", "cursor"}
	},
	"key file mode": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		if runtime.GOOS != "windows" {
			if err := os.Chmod(g.keys.file.Path(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return []string{"--app", "cursor"}
	},
	"no app": func(t *testing.T, g *guideTest) []string { g.serve(t); return nil },
	"app from the chain": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		guideChain = chainOf("/Applications/Cursor.app/Contents/MacOS/Cursor")
		return []string{"--agent", "cursor-b"}
	},
	"node in the terminal of code": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		guideChain = nodeInTheTerminalOfCode
		return nil
	},
	"different app": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		guideChain = chainOf("/Users/a/.local/bin/claude")
		return []string{"--app", "codex"}
	},
	"connected": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		g.addAgent(t)
		connectForTest(t, "cursor")
		return []string{"--app", "cursor"}
	},
	"connected different app": func(t *testing.T, g *guideTest) []string {
		g.serve(t)
		g.addAgent(t)
		connectForTest(t, "cursor")
		guideChain = chainOf("/Users/a/.local/bin/claude")
		return []string{"--app", "cursor"}
	},
}

func setKey(t *testing.T, g *guideTest, account, key string) {
	t.Helper()
	if err := g.keys.file.Set(account, key); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, g *guideTest, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.home, "maihome", "config", "config.json"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func connectForTest(t *testing.T, app string) {
	t.Helper()
	var out, errOut strings.Builder
	if code := runConnect([]string{app, "--agent", app, "--yes"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("connect: %d %s", code, errOut.String())
	}
}

var (
	guideCodeSpan = regexp.MustCompile("`[^`]*`")
	guideParens   = regexp.MustCompile(`\([^()]*\)`)
	// A sentence ends with a period, and the next sentence starts with a capital letter, a
	// digit, or code.
	guideSentenceEnd = regexp.MustCompile(`[.!?]\s+`)
	guideAsk         = regexp.MustCompile(`(?i)\bask(s|ed|ing)?\b`)
)

// guideSentences gives the sentences of one line of the text, with each code span and each
// text in parentheses as one word, as the STE rules count them.
func guideSentences(line string) []string {
	line = guideParens.ReplaceAllString(guideCodeSpan.ReplaceAllString(line, "CODE"), "PARENS")
	var out []string
	for _, s := range guideSentenceEnd.Split(line, -1) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// The text of mai guide is in ASD-STE100 Simplified Technical English in each state: no
// verb "ask", no double period, no sentence of more than 25 words, and no sentence that
// starts with a lower-case word. A sentence that starts with a lower-case word, for example
// "mai start writes ...", joins the sentence before it for a reader and for a linter.
func TestGuideTextIsSimplifiedTechnicalEnglishInEachState(t *testing.T) {
	for name, setup := range guideStates {
		t.Run(name, func(t *testing.T) {
			g := newGuideTest(t)
			args := setup(t, g)
			code, out, errOut := g.run(t, args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			if m := guideAsk.FindString(guideCodeSpan.ReplaceAllString(out, "CODE")); m != "" {
				t.Errorf("the text has the verb %q:\n%s", m, out)
			}
			if strings.Contains(guideCodeSpan.ReplaceAllString(out, "CODE"), "..") {
				t.Errorf("the text has \"..\":\n%s", out)
			}
			for _, line := range strings.Split(out, "\n") {
				for i, s := range guideSentences(line) {
					s = strings.TrimLeft(s, "-0123456789. ")
					if n := len(strings.Fields(s)); n > 25 {
						t.Errorf("a sentence has %d words: %q", n, s)
					}
					// The first sentence of a line can start with the name mai.
					if i > 0 && s != "" && s[0] >= 'a' && s[0] <= 'z' {
						t.Errorf("a sentence starts with a lower-case word: %q in %q", s, line)
					}
				}
			}
		})
	}
}
