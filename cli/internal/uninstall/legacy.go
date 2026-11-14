package uninstall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/moby/moby/client"
)

// Names of the 2.x install.
const (
	LegacyContainer = "mai"
	LegacyProject   = "mai"
	LegacyRepo      = "ghcr.io/monorithm/mai"
)

// LegacyVolumes are the named volumes of the 2.x compose files.
var LegacyVolumes = []string{"mai_mai-data", "mai_mai-spool", "mai_mai-volume"}

// legacyMarkers are parts of the command of a 2.x MCP entry.
var legacyMarkers = []string{".mai/bin/mai", `.mai\bin\mai`, `.mai\\bin\\mai`}

// Legacy removes a 2.x install. It never changes the files of the apps.
type Legacy struct {
	Engine   Engine
	Home     string   // the home folder; the 2.x folder is <Home>/.mai
	AppFiles []string // configuration files of the apps; the uninstall only reads them
	Yes      bool
	In       io.Reader
	Out      io.Writer
}

// LegacyPlan is the list of 2.x items.
type LegacyPlan struct {
	Container string // ID of the compose container; empty = none
	Volumes   []string
	Images    []string // references with a 2.x tag
	Folder    string   // empty = none
	Entries   []string // 2.x MCP entries in files of the apps
}

// SelectContainer gives the ID of the 2.x container "mai", or "". It selects a container only
// when it has the compose project label "mai" and no label of the 3.0 CLI.
func SelectContainer(ctx context.Context, e Engine) (string, error) {
	list, err := e.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("name", "^/"+LegacyContainer+"$"),
	})
	if err != nil {
		return "", err
	}
	for _, c := range list.Items {
		if !hasName(c.Names, LegacyContainer) {
			continue
		}
		if c.Labels[LabelCompose] != LegacyProject {
			continue
		}
		if _, ok := c.Labels[LabelManagedBy]; ok {
			continue
		}
		return c.ID, nil
	}
	return "", nil
}

func legacyTag(ref string) (string, bool) {
	i := strings.LastIndex(ref, ":")
	if i < 0 || ref[:i] != LegacyRepo {
		return "", false
	}
	tag := ref[i+1:]
	return tag, strings.HasPrefix(tag, "2")
}

func (l *Legacy) out() io.Writer {
	if l.Out == nil {
		return io.Discard
	}
	return l.Out
}

// MakePlan finds the 2.x items.
func (l *Legacy) MakePlan(ctx context.Context) (*LegacyPlan, error) {
	p := &LegacyPlan{}
	id, err := SelectContainer(ctx, l.Engine)
	if err != nil {
		return nil, err
	}
	p.Container = id
	for _, v := range LegacyVolumes {
		r, err := l.Engine.VolumeInspect(ctx, v, client.VolumeInspectOptions{})
		if IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, ok := r.Volume.Labels[LabelManagedBy]; ok {
			continue
		}
		p.Volumes = append(p.Volumes, v)
	}
	imgs, err := l.Engine.ImageList(ctx, client.ImageListOptions{Filters: client.Filters{}.Add("reference", LegacyRepo)})
	if err != nil {
		return nil, err
	}
	for _, im := range imgs.Items {
		for _, ref := range im.RepoTags {
			if _, ok := legacyTag(ref); ok {
				p.Images = append(p.Images, ref)
			}
		}
	}
	sort.Strings(p.Images)
	if l.Home != "" {
		dir := filepath.Join(l.Home, ".mai")
		if _, err := os.Lstat(dir); err == nil {
			p.Folder = dir
		}
	}
	for _, f := range l.AppFiles {
		p.Entries = append(p.Entries, FindLegacyEntries(f)...)
	}
	return p, nil
}

// FindLegacyEntries reads one configuration file of an app and gives each 2.x MCP entry in
// it. It never changes the file.
func FindLegacyEntries(file string) []string {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var doc any
	if json.Unmarshal(b, &doc) == nil {
		var found []string
		walkJSON(doc, "", func(ptr string, obj map[string]any) {
			if cmd, ok := obj["command"].(string); ok && isLegacyCommand(cmd) {
				found = append(found, file+" "+ptr)
			}
		})
		sort.Strings(found)
		return found
	}
	var found []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		if isLegacyCommand(sc.Text()) {
			found = append(found, fmt.Sprintf("%s line %d", file, n))
		}
	}
	return found
}

func isLegacyCommand(s string) bool {
	for _, m := range legacyMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func walkJSON(v any, ptr string, visit func(string, map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		visit(ptr, x)
		for k, child := range x {
			esc := strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
			walkJSON(child, ptr+"/"+esc, visit)
		}
	case []any:
		for i, child := range x {
			walkJSON(child, fmt.Sprintf("%s/%d", ptr, i), visit)
		}
	}
}

// Empty tells if the plan has no item to remove.
func (p *LegacyPlan) Empty() bool {
	return p.Container == "" && len(p.Volumes) == 0 && len(p.Images) == 0 && p.Folder == ""
}

// Show writes the 2.x items.
func (l *Legacy) Show(p *LegacyPlan) {
	w := l.out()
	fmt.Fprintln(w, "mai uninstall --legacy removes these items of mai 2.x:")
	if p.Container != "" {
		fmt.Fprintf(w, "  container  %s (compose project %s, with its anonymous volumes)\n", LegacyContainer, LegacyProject)
	}
	for _, v := range p.Volumes {
		fmt.Fprintf(w, "  volume     %s\n", v)
	}
	for _, im := range p.Images {
		fmt.Fprintf(w, "  image      %s\n", im)
	}
	if p.Folder != "" {
		fmt.Fprintf(w, "  folder     %s (shims, compose.yaml, credential files)\n", p.Folder)
	}
	fmt.Fprintln(w, "CAUTION: this deletes all 2.x mail. The CLI does not read or move 2.x data.")
}

// Run removes the 2.x install and returns the exit code.
func (l *Legacy) Run(ctx context.Context) int {
	w := l.out()
	p, err := l.MakePlan(ctx)
	if err != nil {
		fmt.Fprintln(w, "mai uninstall --legacy:", err)
		return 1
	}
	if p.Empty() {
		fmt.Fprintln(w, "No item of mai 2.x was found.")
	} else {
		l.Show(p)
		if !Confirm(l.In, w, l.Yes) {
			fmt.Fprintln(w, "mai uninstall --legacy: stopped. No item was removed.")
			return 1
		}
	}
	rs := l.Remove(ctx, p)
	Report(w, rs)
	if len(p.Entries) > 0 {
		fmt.Fprintln(w, "These files of apps have 2.x MCP entries. Remove them yourself:")
		for _, e := range p.Entries {
			fmt.Fprintln(w, "  "+e)
		}
	}
	if Failed(rs) {
		return 1
	}
	return 0
}

// Remove removes the items of the plan and does a check of each one.
func (l *Legacy) Remove(ctx context.Context, p *LegacyPlan) []Result {
	var rs []Result
	if p.Container != "" {
		id := p.Container
		_, err := l.Engine.ContainerRemove(ctx, id, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true})
		rs = append(rs, check("container "+LegacyContainer, ignoreNotFound(err), func() (bool, error) {
			_, e := l.Engine.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
			return engineExists(e)
		}))
	}
	for _, v := range p.Volumes {
		_, err := l.Engine.VolumeRemove(ctx, v, client.VolumeRemoveOptions{})
		rs = append(rs, check("volume "+v, ignoreNotFound(err), func() (bool, error) {
			_, e := l.Engine.VolumeInspect(ctx, v, client.VolumeInspectOptions{})
			return engineExists(e)
		}))
	}
	for _, im := range p.Images {
		_, err := l.Engine.ImageRemove(ctx, im, client.ImageRemoveOptions{PruneChildren: true})
		rs = append(rs, check("image "+im, ignoreNotFound(err), func() (bool, error) {
			r, e := l.Engine.ImageInspect(ctx, im)
			if IsNotFound(e) {
				return false, nil
			}
			if e != nil {
				return false, e
			}
			for _, t := range r.RepoTags {
				if t == im {
					return true, nil
				}
			}
			return false, nil
		}))
	}
	if p.Folder != "" {
		err := os.RemoveAll(p.Folder)
		rs = append(rs, check("folder "+p.Folder, err, func() (bool, error) {
			_, e := os.Lstat(p.Folder)
			if errors.Is(e, os.ErrNotExist) {
				return false, nil
			}
			return e == nil, e
		}))
	}
	return rs
}

// LegacyAppFiles gives the configuration files of the apps that can hold a 2.x MCP entry.
func LegacyAppFiles(home, goos string) []string {
	files := []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".cursor", "mcp.json"),
		filepath.Join(codexHome(home), "config.toml"),
		filepath.Join(home, ".gemini", "settings.json"),
		filepath.Join(home, ".gemini", "config", "mcp_config.json"),
		filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
		filepath.Join(home, ".copilot", "mcp-config.json"),
	}
	switch goos {
	case "darwin":
		files = append(files, filepath.Join(home, "Library", "Application Support", "Code", "User", "mcp.json"))
	case "windows":
		if app := os.Getenv("AppData"); app != "" {
			files = append(files, filepath.Join(app, "Code", "User", "mcp.json"))
		}
	default:
		files = append(files, filepath.Join(home, ".config", "Code", "User", "mcp.json"))
	}
	return files
}

// codexHome is the folder of Codex: CODEX_HOME when it is set, else ~/.codex.
func codexHome(home string) string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(home, ".codex")
}
