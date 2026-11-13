package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
	"github.com/monorithm/mai/cli/v3/internal/version"
)

func init() {
	register("start", "Start the mail server. Make the container on the first start.", runStart)
	register("stop", "Stop the container. The data stays.", runStop)
	register("pause", "Freeze the container", runPause)
	register("resume", "Resume a frozen container", runResume)
	register("status", "Show the health, the versions, the engine, the data, and the port", runStatus)
	register("logs", "Show the log of the container", runLogs)
	register("exec", "Run the server CLI in the container, for recovery", runExec)
	register("upgrade", "Get the image of this CLI version, make the container again, and move the keys from the keychain", runUpgrade)
	register("doctor", "Examine the engine, the port, the disk, the key file, and the legacy keychain items", runDoctor)
}

// Seams for the tests.
var (
	lcOut           io.Writer = os.Stdout
	lcErr           io.Writer = os.Stderr
	lcHealthWait              = engine.StartWait
	lcConnectEngine           = engine.ConnectSystem
)

// lcOwnerKeyExists is the exit code of "mai owner set-key" when the server has a key.
const lcOwnerKeyExists = 3

// lcMinFreeBytes is the free disk space under which doctor reports a problem.
const lcMinFreeBytes = 2 << 30

// lcSession holds the state of one lifecycle command.
type lcSession struct {
	dirs paths.Dirs
	cfg  config.Config
	eng  *engine.Engine
}

func lcFail(format string, a ...any) int {
	fmt.Fprintf(lcErr, "mai: "+format+"\n", a...)
	return 1
}

// lcInstanceFlags adds the flags that select the instance.
func lcInstanceFlags(fs *flag.FlagSet, o *config.Overrides) {
	fs.StringVar(&o.Instance, "instance", "", "name of the instance and of its container")
	fs.IntVar(&o.Port, "port", 0, "host port of the HTTP API on 127.0.0.1")
}

func lcFlags(name string, hidden ...string) *flag.FlagSet {
	fs := flag.NewFlagSet("mai "+name, flag.ContinueOnError)
	fs.SetOutput(lcErr)
	fs.Usage = func() {
		fmt.Fprintf(lcErr, "usage: mai %s [FLAGS]\n", name)
		fs.VisitAll(func(f *flag.Flag) {
			for _, h := range hidden {
				if f.Name == h {
					return
				}
			}
			fmt.Fprintf(lcErr, "  --%s  %s\n", f.Name, f.Usage)
		})
	}
	return fs
}

func lcOpen(o config.Overrides, connect bool) (*lcSession, error) {
	dirs, err := paths.Default()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(dirs.Config)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", config.Path(dirs.Config), err)
	}
	s := &lcSession{dirs: dirs, cfg: cfg.Apply(o)}
	if !connect {
		return s, nil
	}
	s.eng, err = lcConnectEngine(context.Background(), lcErr)
	if errors.Is(err, engine.ErrNotInstalled) {
		return nil, errors.New("no container engine is installed. " + engine.InstallHint(runtime.GOOS))
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *lcSession) close() {
	if s.eng != nil {
		_ = s.eng.Close()
	}
}

func (s *lcSession) manifest() (*manifest.Manifest, error) {
	return manifest.Load(filepath.Join(s.dirs.State, manifest.FileName), s.cfg.Instance)
}

func (s *lcSession) spec() engine.Spec {
	return engine.Spec{Instance: s.cfg.Instance, Port: s.cfg.Port, Image: s.cfg.Image, DataDir: s.cfg.DataDir}
}

// lcLegacyMessage is the text for a container that the CLI did not make.
func lcLegacyMessage(name string) string {
	return fmt.Sprintf("a container %q exists that mai 3 did not make. A 2.x install holds the name. Run \"mai uninstall --legacy\" to remove the 2.x install, or use --instance NAME.", name)
}

// owned finds the container of the instance and refuses a container of a 2.x install.
func (s *lcSession) owned(ctx context.Context) (container.InspectResponse, bool, error) {
	c, ok, err := s.eng.Container(ctx, s.cfg.Instance)
	if err != nil || !ok {
		return c, ok, err
	}
	if engine.Owned(c) != nil {
		return c, true, errors.New(lcLegacyMessage(s.cfg.Instance))
	}
	return c, true, nil
}

// lcNewOwnerKey makes an owner key in the format mai1:owner:<secret>.
func lcNewOwnerKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	return "mai1:owner:" + secret, nil
}

// setOwner keeps the owner key in the key file and gives it to the server on stdin. The
// server keeps only a hash. A legacy keychain item of the owner key moves to the key file.
func (s *lcSession) setOwner(ctx context.Context, id string) error {
	kf, err := openKeys()
	if err != nil {
		return err
	}
	account := keys.OwnerAccount(s.cfg.Instance)
	key, err := kf.Get(account)
	made := false
	if errors.Is(err, keys.ErrNotFound) {
		fresh, err := lcNewOwnerKey()
		if err != nil {
			return err
		}
		// A different process can write an owner key after the read. Then that key stays, and
		// the server gets it.
		if key, made, err = kf.Add(account, fresh); err != nil {
			return fmt.Errorf("keep the owner key in the key file: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read the owner key: %w", err)
	}
	code, err := s.eng.Exec(ctx, id, []string{"mai", "owner", "set-key"}, strings.NewReader(key+"\n"), io.Discard, lcErr)
	switch {
	case err != nil:
		return fmt.Errorf("mai owner set-key: %w", err)
	case code == 0:
		fmt.Fprintln(lcOut, "mai: the owner key is set")
	case code == lcOwnerKeyExists && made:
		fmt.Fprintln(lcErr, "mai: the server has a different owner key. To give it the key of the key file, run: mai exec -- mai owner set-key --replace")
	case code == lcOwnerKeyExists:
	default:
		return fmt.Errorf("mai owner set-key gave exit code %d", code)
	}
	return nil
}

func lcPortFree(port int) bool {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// create makes the volumes and the container, and records them in the manifest.
func (s *lcSession) create(ctx context.Context, noPull bool, m *manifest.Manifest) (string, error) {
	sp := s.spec()
	if !lcPortFree(sp.Port) {
		return "", fmt.Errorf("the port 127.0.0.1:%d is in use. Stop the program that uses it, or use --port N", sp.Port)
	}
	imageID, pulled, err := s.eng.EnsureImage(ctx, sp.Image, noPull, lcErr)
	if errors.Is(err, engine.ErrPullRefused) {
		return "", fmt.Errorf("the image %s is not local, and --no-pull refuses a pull", sp.Image)
	}
	if err != nil {
		return "", fmt.Errorf("get the image %s: %w", sp.Image, err)
	}
	if pulled {
		if err := m.Add(manifest.Item{Type: manifest.Image, Name: sp.Image, ID: imageID}); err != nil {
			return "", err
		}
	}
	if _, err := s.eng.EnsureVolumes(ctx, sp); err != nil {
		return "", fmt.Errorf("make the volumes: %w", err)
	}
	for _, v := range sp.Volumes() {
		if err := m.Add(manifest.Item{Type: manifest.Volume, Name: v}); err != nil {
			return "", err
		}
	}
	if sp.DataDir != "" {
		if err := os.MkdirAll(sp.DataDir, 0o700); err != nil {
			return "", err
		}
		if err := m.Add(manifest.Item{Type: manifest.Folder, Name: "data", Path: sp.DataDir}); err != nil {
			return "", err
		}
	}
	id, err := s.eng.Create(ctx, sp)
	if err != nil {
		return "", fmt.Errorf("make the container: %w", err)
	}
	return id, m.Add(manifest.Item{Type: manifest.Container, Name: sp.Instance, ID: id})
}

// up starts the container and waits for its health.
func (s *lcSession) up(ctx context.Context, c container.InspectResponse, id string) error {
	switch {
	case c.State != nil && c.State.Paused:
		if err := s.eng.Resume(ctx, id); err != nil {
			return err
		}
	case c.State == nil || !c.State.Running:
		if err := s.eng.Start(ctx, id); err != nil {
			return fmt.Errorf("start the container: %w", err)
		}
	}
	fmt.Fprintln(lcErr, "mai: wait for the health of the server")
	return s.eng.WaitHealthy(ctx, id, lcHealthWait)
}

func lcRecordFolders(s *lcSession, m *manifest.Manifest) error {
	if err := m.Add(manifest.Item{Type: manifest.Folder, Name: "config", Path: s.dirs.Config}); err != nil {
		return err
	}
	return m.Add(manifest.Item{Type: manifest.Folder, Name: "state", Path: s.dirs.State})
}

func runStart(args []string) int {
	fs := lcFlags("start", "skip-owner")
	var o config.Overrides
	lcInstanceFlags(fs, &o)
	fs.StringVar(&o.Image, "image", "", "image reference of the server")
	fs.StringVar(&o.DataDir, "data-dir", "", "host folder for /data in place of a volume")
	noPull := fs.Bool("no-pull", false, "refuse to pull the image")
	skipOwner := fs.Bool("skip-owner", false, "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if o.DataDir != "" {
		abs, err := filepath.Abs(o.DataDir)
		if err != nil {
			return lcFail("%v", err)
		}
		o.DataDir = abs
	}
	dirs, err := paths.Default()
	if err != nil {
		return lcFail("%v", err)
	}
	if o != (config.Overrides{}) {
		if err := config.Update(dirs.Config, o); err != nil {
			return lcFail("write the configuration: %v", err)
		}
	}
	s, err := lcOpen(o, true)
	if err != nil {
		return lcFail("%v", err)
	}
	defer s.close()
	ctx := context.Background()
	m, err := s.manifest()
	if err != nil {
		return lcFail("read the manifest: %v", err)
	}
	if err := lcRecordFolders(s, m); err != nil {
		return lcFail("%v", err)
	}
	c, ok, err := s.owned(ctx)
	if err != nil {
		return lcFail("%v", err)
	}
	id := c.ID
	if !ok {
		if id, err = s.create(ctx, *noPull, m); err != nil {
			return lcFail("%v", err)
		}
		c = container.InspectResponse{}
	}
	if err := s.up(ctx, c, id); err != nil {
		return lcFail("%v", err)
	}
	if !*skipOwner {
		if err := s.setOwner(ctx, id); err != nil {
			return lcFail("%v", err)
		}
	}
	fmt.Fprintf(lcOut, "mai: the server is healthy at http://127.0.0.1:%d\n", s.cfg.Port)
	return 0
}

// simple runs one call on the container of the instance.
func lcSimple(name string, args []string, call func(*engine.Engine, context.Context, string) error, done string) int {
	fs := lcFlags(name)
	var o config.Overrides
	fs.StringVar(&o.Instance, "instance", "", "name of the instance and of its container")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, err := lcOpen(o, true)
	if err != nil {
		return lcFail("%v", err)
	}
	defer s.close()
	ctx := context.Background()
	c, ok, err := s.owned(ctx)
	if err != nil {
		return lcFail("%v", err)
	}
	if !ok {
		return lcFail("no container %q exists. Run: mai start", s.cfg.Instance)
	}
	if err := call(s.eng, ctx, c.ID); err != nil {
		return lcFail("%s: %v", name, err)
	}
	fmt.Fprintf(lcOut, "mai: %s\n", done)
	return 0
}

func runStop(args []string) int {
	return lcSimple("stop", args, (*engine.Engine).Stop, "the container is stopped")
}

func runPause(args []string) int {
	return lcSimple("pause", args, (*engine.Engine).Pause, "the container is paused")
}

func runResume(args []string) int {
	return lcSimple("resume", args, (*engine.Engine).Resume, "the container runs")
}

// lcAPIGet calls the HTTP API of the server. key is the owner key, or "" for no auth.
func lcAPIGet(port int, path, key string, out any) error {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	if err != nil {
		return err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s gave HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func runStatus(args []string) int {
	fs := lcFlags("status")
	var o config.Overrides
	lcInstanceFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, err := lcOpen(o, true)
	if err != nil {
		return lcFail("%v", err)
	}
	defer s.close()
	ctx := context.Background()
	p := func(k, v string) { fmt.Fprintf(lcOut, "%-10s %s\n", k+":", v) }
	p("cli", version.Version)
	p("engine", s.eng.Describe(ctx))
	p("port", "127.0.0.1:"+strconv.Itoa(s.cfg.Port))
	if s.cfg.DataDir != "" {
		p("data", s.cfg.DataDir)
	} else {
		p("data", "volume "+engine.DataVolume(s.cfg.Instance))
	}
	c, ok, err := s.owned(ctx)
	if err != nil {
		return lcFail("%v", err)
	}
	if !ok {
		p("container", "none. Run: mai start")
		return 1
	}
	state := "unknown"
	if c.State != nil {
		state = string(c.State.Status)
	}
	p("container", fmt.Sprintf("%s (%s)", s.cfg.Instance, state))
	if c.Config != nil {
		p("image", c.Config.Image)
	}
	var v struct {
		Version  string `json:"version"`
		Contract string `json:"contract"`
	}
	if err := lcAPIGet(s.cfg.Port, "/api/version", "", &v); err == nil {
		p("server", fmt.Sprintf("%s (contract %s)", v.Version, v.Contract))
	}
	if c.State == nil || !c.State.Running || c.State.Paused {
		p("health", "not running")
		return 1
	}
	healthy := s.eng.Healthy(ctx, c.ID, nil) == nil
	if healthy {
		p("health", "healthy")
	} else {
		p("health", "not healthy. Run: mai logs")
	}
	if v.Version != "" {
		if key := lcOwnerKey(s.cfg.Instance); key != "" {
			var ui struct {
				Enabled bool `json:"enabled"`
			}
			if err := lcAPIGet(s.cfg.Port, "/api/ui", key, &ui); err == nil {
				p("ui", map[bool]string{true: "on", false: "off"}[ui.Enabled])
			}
		}
	}
	if !healthy {
		return 1
	}
	return 0
}

// lcOwnerKey gives the owner key of the instance, or "" when the key file has none.
func lcOwnerKey(instance string) string {
	kf, err := openKeys()
	if err != nil {
		return ""
	}
	key, err := kf.Get(keys.OwnerAccount(instance))
	if err != nil {
		return ""
	}
	return key
}

func runLogs(args []string) int {
	fs := lcFlags("logs")
	var o config.Overrides
	fs.StringVar(&o.Instance, "instance", "", "name of the instance and of its container")
	follow := fs.Bool("follow", false, "keep the log open and show new lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, err := lcOpen(o, true)
	if err != nil {
		return lcFail("%v", err)
	}
	defer s.close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c, ok, err := s.owned(ctx)
	if err != nil {
		return lcFail("%v", err)
	}
	if !ok {
		return lcFail("no container %q exists. Run: mai start", s.cfg.Instance)
	}
	if err := s.eng.Logs(ctx, c.ID, *follow, lcOut, lcErr); err != nil && ctx.Err() == nil {
		return lcFail("logs: %v", err)
	}
	return 0
}

func runExec(args []string) int {
	fs := lcFlags("exec")
	var o config.Overrides
	fs.StringVar(&o.Instance, "instance", "", "name of the instance and of its container")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return lcFail("usage: mai exec -- ARGS")
	}
	s, err := lcOpen(o, true)
	if err != nil {
		return lcFail("%v", err)
	}
	defer s.close()
	ctx := context.Background()
	c, ok, err := s.owned(ctx)
	if err != nil {
		return lcFail("%v", err)
	}
	if !ok {
		return lcFail("no container %q exists. Run: mai start", s.cfg.Instance)
	}
	code, err := s.eng.Exec(ctx, c.ID, rest, os.Stdin, lcOut, lcErr)
	if err != nil {
		return lcFail("exec: %v", err)
	}
	return code
}

func runUpgrade(args []string) int {
	fs := lcFlags("upgrade", "skip-owner")
	var o config.Overrides
	fs.StringVar(&o.Instance, "instance", "", "name of the instance and of its container")
	fs.StringVar(&o.Image, "image", "", "image reference; the default is the image of this CLI version")
	noPull := fs.Bool("no-pull", false, "refuse to pull the image")
	skipOwner := fs.Bool("skip-owner", false, "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if o.Image == "" {
		o.Image = config.DefaultImage()
	}
	s, err := lcOpen(o, true)
	if err != nil {
		return lcFail("%v", err)
	}
	defer s.close()
	ctx := context.Background()
	m, err := s.manifest()
	if err != nil {
		return lcFail("read the manifest: %v", err)
	}
	// The move of the keys comes first, thus it also occurs when the image does not change.
	exit := 0
	if !lcMoveKeys(s.cfg.Instance, m) {
		exit = 1
	}
	c, ok, err := s.owned(ctx)
	if err != nil {
		return lcFail("%v", err)
	}
	imageID, pulled, err := s.eng.EnsureImage(ctx, s.cfg.Image, *noPull, lcErr)
	if err != nil {
		return lcFail("get the image %s: %v", s.cfg.Image, err)
	}
	if pulled {
		if err := m.Add(manifest.Item{Type: manifest.Image, Name: s.cfg.Image, ID: imageID}); err != nil {
			return lcFail("%v", err)
		}
	}
	if ok {
		if c.Image == imageID {
			fmt.Fprintf(lcOut, "mai: the container has the image %s\n", s.cfg.Image)
			return exit
		}
		fmt.Fprintln(lcErr, "mai: stop the container")
		if err := s.eng.Stop(ctx, c.ID); err != nil {
			return lcFail("stop: %v", err)
		}
		if err := s.eng.Remove(ctx, c.ID); err != nil {
			return lcFail("remove the old container: %v", err)
		}
		if err := m.Remove(manifest.Item{Type: manifest.Container, Name: s.cfg.Instance}); err != nil {
			return lcFail("%v", err)
		}
	}
	id, err := s.create(ctx, true, m)
	if err != nil {
		return lcFail("%v", err)
	}
	if err := s.up(ctx, container.InspectResponse{}, id); err != nil {
		return lcFail("%v", err)
	}
	if !*skipOwner {
		if err := s.setOwner(ctx, id); err != nil {
			return lcFail("%v", err)
		}
	}
	pin := o.Image
	if pin == config.DefaultImage() {
		pin = ""
	}
	if err := config.PinImage(s.dirs.Config, pin); err != nil {
		return lcFail("write the configuration: %v", err)
	}
	fmt.Fprintf(lcOut, "mai: the server runs %s\n", s.cfg.Image)
	return exit
}

// lcRecordedKeys gives the account of each legacy keychain item in the manifest. mai 3.0
// and 3.1 recorded each item that they wrote.
func lcRecordedKeys(m *manifest.Manifest) []string {
	var out []string
	for _, it := range m.Of(manifest.KeychainKey) {
		out = append(out, it.Name)
	}
	return out
}

// lcMoveKeys moves each key of the instance from the legacy keychain to the key file. It
// removes a legacy item whose account the key file has only when the item holds the same key,
// or when the server accepts only the key of the key file. It removes the record of each
// legacy item that is gone from the manifest. It gives false when a key did not move, or when
// a legacy item stays.
func lcMoveKeys(instance string, m *manifest.Manifest) bool {
	kf, err := openKeys()
	if err != nil {
		fmt.Fprintf(lcErr, "mai: the key file: %v\n", err)
		return false
	}
	if !kf.HasLegacy() {
		return true
	}
	recorded := lcRecordedKeys(m)
	candidates, err := kf.Candidates(instance, recorded)
	if err != nil {
		fmt.Fprintf(lcErr, "mai: the key file: %v\n", err)
		return false
	}
	r, err := kf.MoveAll(candidates, lcOwnerCheck(instance))
	if len(r.Moved) > 0 {
		fmt.Fprintf(lcOut, "mai: moved %d key(s) from the keychain to the key file %s: %s\n",
			len(r.Moved), kf.Path(), strings.Join(r.Moved, ", "))
	}
	if len(r.Cleaned) > 0 {
		fmt.Fprintf(lcOut, "mai: removed %d old keychain item(s), because the key file has the same key, "+
			"or because the server accepts only the key of the key file: %s\n",
			len(r.Cleaned), strings.Join(r.Cleaned, ", "))
	}
	for _, c := range r.Conflicts {
		fmt.Fprintf(lcErr, "mai: the keychain item %s stays: %s\n", c.Account, lcConflictText(instance, kf.Path(), c))
	}
	for _, st := range r.Stays {
		fmt.Fprintf(lcErr, "mai: the keychain item %s stays, because its delete failed: %v. "+
			"The key file %s has the key of this account. %s\n", st.Account, st.Err, kf.Path(), lcStayStep(st.Account))
	}
	var mine []string
	for _, a := range recorded {
		if keys.InstanceOf(a) == instance {
			mine = append(mine, a)
		}
	}
	if r.Unavailable && len(mine) > 0 {
		// mai 3.0 and 3.1 did not operate with no keychain. Thus only a recorded item can exist.
		fmt.Fprintf(lcErr, "mai: the keychain is not available, thus the keys of mai 3.0 or 3.1 did not move: %s. "+
			"The manifest records these keychain items. Make the keychain available, for example in a desktop session of the user, "+
			"and then run mai upgrade again.\n", strings.Join(mine, ", "))
		return false
	}
	for _, it := range m.Of(manifest.KeychainKey) {
		if keys.InstanceOf(it.Name) != instance {
			continue
		}
		if has, herr := kf.HasLegacyItem(it.Name); herr == nil && !has {
			_ = m.Remove(it)
		}
	}
	if err != nil {
		fmt.Fprintf(lcErr, "mai: a key did not move from the keychain: %v. Run mai upgrade again.\n", err)
		return false
	}
	if len(r.Conflicts) > 0 || len(r.Stays) > 0 {
		return false
	}
	if r.Unavailable {
		// With no keychain and no record, no item exists.
		return true
	}
	// The move is complete only when no item of the instance stays.
	done := true
	for _, a := range candidates {
		switch has, herr := kf.HasLegacyItem(a); {
		case herr != nil:
			fmt.Fprintf(lcErr, "mai: the check of the keychain item %s failed: %v. "+
				"Step: unlock the keychain, and then run mai upgrade again.\n", a, herr)
			done = false
		case has:
			fmt.Fprintf(lcErr, "mai: the keychain item %s stays after the move. %s\n", a, lcStayStep(a))
			done = false
		}
	}
	return done
}

// lcStayStep gives the step for a keychain item that stays, although the key file has the key
// of its account.
func lcStayStep(account string) string {
	return fmt.Sprintf("Step: unlock the keychain, and then run mai upgrade again. When macOS shows a prompt, let mai delete the item. "+
		"Or delete the keychain item yourself: the service %s, the account %s.", keychain.Service, account)
}

// errNoAgentCheck means that the CLI has no check of an agent key on the server. A lease
// request with the key is not a check, because it takes a session.
var errNoAgentCheck = errors.New("the CLI cannot ask the server about an agent key")

// lcOwnerCheck gives the check of the server for a legacy item whose key is not the key of the
// key file. Only the owner key has a check: an owner API call with the key.
func lcOwnerCheck(instance string) keys.Accepts {
	return func(account, key string) (bool, error) {
		if account != keys.OwnerAccount(instance) {
			return false, errNoAgentCheck
		}
		dirs, err := paths.Default()
		if err != nil {
			return false, err
		}
		cfg, err := config.Load(dirs.Config)
		if err != nil {
			return false, err
		}
		return lcOwnerKeyAccepted(cfg.Port, key)
	}
}

// lcOwnerKeyAccepted tells if the server at the port accepts the owner key. It gives an error
// when the server gives no answer, or an answer that is not 200 or 401.
func lcOwnerKeyAccepted(port int, key string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/api/agents", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized:
		return false, nil
	}
	return false, fmt.Errorf("GET /api/agents gave HTTP %d", resp.StatusCode)
}

// lcConflictText tells why a legacy item stays, and gives the step for the user.
func lcConflictText(instance, path string, c keys.Conflict) string {
	owner := keys.OwnerAccount(instance)
	if c.Account != owner {
		name := strings.TrimSuffix(strings.TrimPrefix(c.Account, "agent:"), "@"+instance)
		return fmt.Sprintf("the key file %s has a different key for this account. "+
			"Step: run mai agent rotate %s. The server then accepts only the new key, and the command removes the keychain item.",
			path, name)
	}
	switch {
	case c.Err != nil:
		return fmt.Sprintf("the key file %s has a different owner key, and the check of the server failed: %v. "+
			"Step: start the server with mai start, and then run mai upgrade again.", path, c.Err)
	case c.LegacyAccepted && !c.FileAccepted:
		return fmt.Sprintf("the server accepts the key of the keychain item, and it refuses the owner key of the key file %s. "+
			"Step: remove the account %s from the key file, and then run mai upgrade again. "+
			"The move then puts the key of the keychain item in the key file.", path, owner)
	case !c.LegacyAccepted && !c.FileAccepted:
		return fmt.Sprintf("the key file %s has a different owner key, and the server refuses the two keys. "+
			"Step: give the owner key of the key file to the server with mai exec -- mai owner set-key --replace, "+
			"which reads the key on stdin. Then run mai upgrade again.", path)
	}
	return fmt.Sprintf("the key file %s has a different owner key, and the server accepts the two keys. "+
		"Step: run mai upgrade again.", path)
}

func runDoctor(args []string) int {
	fs := lcFlags("doctor")
	var o config.Overrides
	lcInstanceFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, err := lcOpen(o, false)
	if err != nil {
		return lcFail("%v", err)
	}
	problems, warnings := 0, 0
	ok := func(k, v string) { fmt.Fprintf(lcOut, "ok      %-9s %s\n", k, v) }
	warn := func(k, v, step string) {
		warnings++
		fmt.Fprintf(lcOut, "warning %-9s %s\n        step: %s\n", k, v, step)
	}
	bad := func(k, v, step string) {
		problems++
		fmt.Fprintf(lcOut, "problem %-9s %s\n        step: %s\n", k, v, step)
	}
	ctx := context.Background()
	g := runtime.GOOS
	eng, err := engine.Find(ctx, engine.Discover(engine.SystemEnv(g)))
	switch {
	case err == nil:
		s.eng = eng
		defer s.close()
		ok("engine", eng.Describe(ctx))
	case len(engine.Launchers(g)) == 0:
		bad("engine", "no container engine is installed", engine.InstallHint(g))
	default:
		bad("engine", "the container engine gives no response", "run: mai start (it starts the engine)")
	}
	port := "127.0.0.1:" + strconv.Itoa(s.cfg.Port)
	switch {
	case lcPortFree(s.cfg.Port):
		ok("port", port+" is free")
	case s.eng != nil && s.portIsMai(ctx):
		ok("port", port+" is in use by mai")
	default:
		bad("port", port+" is in use by a different program", "stop that program, or run: mai start --port N")
	}
	if err := os.MkdirAll(s.dirs.State, 0o700); err == nil {
		if free, err := engine.FreeBytes(s.dirs.State); err != nil {
			bad("disk", err.Error(), "make sure that the state folder "+s.dirs.State+" exists")
		} else if free < lcMinFreeBytes {
			bad("disk", fmt.Sprintf("%d MB free", free>>20), "make at least 2 GB of disk space free")
		} else {
			ok("disk", fmt.Sprintf("%d GB free", free>>30))
		}
	}
	lcDoctorKeys(s, ok, warn, bad)
	summary := "no problems"
	if problems > 0 {
		summary = fmt.Sprintf("%d problem(s)", problems)
	}
	if warnings > 0 {
		summary += fmt.Sprintf(", %d warning(s)", warnings)
	}
	fmt.Fprintln(lcOut, summary)
	if problems > 0 {
		return 1
	}
	return 0
}

func (s *lcSession) portIsMai(ctx context.Context) bool {
	c, ok, err := s.owned(ctx)
	return err == nil && ok && c.State != nil && c.State.Running
}

// lcDoctorKeys examines the key file and the legacy keychain items of the instance. It reads
// no key, and macOS shows no prompt.
func lcDoctorKeys(s *lcSession, ok func(k, v string), warn, bad func(k, v, step string)) {
	inst := s.cfg.Instance
	kf, err := openKeys()
	if err != nil {
		bad("keys", err.Error(), "make sure that the home folder of the user exists")
		return
	}
	var me *keys.ModeError
	switch err := kf.Check(); {
	case errors.As(err, &me):
		bad("keys", err.Error(), fmt.Sprintf("run: chmod 600 %q", me.Path))
		return
	case err != nil:
		bad("keys", err.Error(), "correct the key file "+kf.Path()+", or move it to a different folder")
		return
	}
	mine, err := kf.OfInstance(inst)
	if err != nil {
		bad("keys", err.Error(), "correct the key file "+kf.Path())
		return
	}
	ok("keys", fmt.Sprintf("%s holds %d key(s) of the instance %s", kf.Path(), len(mine), inst))
	if !kf.HasLegacy() {
		ok("legacy", "MAI_HOME is set, thus the CLI uses no keychain item")
		return
	}
	m, err := s.manifest()
	if err != nil {
		bad("legacy", "read the manifest: "+err.Error(), "correct the manifest in "+s.dirs.State)
		return
	}
	candidates, err := kf.Candidates(inst, lcRecordedKeys(m))
	if err != nil {
		bad("legacy", err.Error(), "correct the key file "+kf.Path())
		return
	}
	var recorded []string
	for _, a := range lcRecordedKeys(m) {
		if keys.InstanceOf(a) == inst {
			recorded = append(recorded, a)
		}
	}
	items, err := kf.LegacyItems(candidates)
	switch {
	case errors.Is(err, keychain.ErrUnavailable) && len(recorded) > 0:
		// The items can exist, but no check is possible.
		warn("legacy", fmt.Sprintf("the keychain is not available, and the manifest records %d keychain item(s) of mai 3.0 or 3.1 for the instance %s: %s",
			len(recorded), inst, strings.Join(recorded, ", ")),
			"make the keychain available, for example in a desktop session of the user, and then run: mai upgrade")
	case errors.Is(err, keychain.ErrUnavailable):
		// mai 3.0 and 3.1 did not operate with no keychain. Thus with no record, no item exists.
		ok("legacy", "no keychain is available, thus it holds no key of mai")
	case err != nil:
		bad("legacy", "the examination of the keychain failed: "+err.Error(), "unlock the login keychain, then run mai doctor again")
	case len(items) > 0:
		bad("legacy", fmt.Sprintf("%d key(s) of the instance %s are in the keychain of mai 3.0 or 3.1 (service %s): %s",
			len(items), inst, keychain.Service, strings.Join(items, ", ")),
			"run: mai upgrade. It moves the keys to the key file and removes the keychain items")
	default:
		ok("legacy", "the keychain holds no key of the instance "+inst)
	}
}
