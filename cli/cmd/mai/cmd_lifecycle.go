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

	"github.com/monorithm/mai/cli/internal/config"
	"github.com/monorithm/mai/cli/internal/engine"
	"github.com/monorithm/mai/cli/internal/keychain"
	"github.com/monorithm/mai/cli/internal/manifest"
	"github.com/monorithm/mai/cli/internal/paths"
	"github.com/monorithm/mai/cli/internal/presence"
	"github.com/monorithm/mai/cli/internal/version"
)

func init() {
	register("start", "Start the mail server. Make the container on the first start.", runStart)
	register("stop", "Stop the container. The data stays.", runStop)
	register("pause", "Freeze the container", runPause)
	register("resume", "Resume a frozen container", runResume)
	register("status", "Show the health, the versions, the engine, the data, and the port", runStatus)
	register("logs", "Show the log of the container", runLogs)
	register("exec", "Run the server CLI in the container, for recovery", runExec)
	register("upgrade", "Get the image of this CLI version and make the container again", runUpgrade)
	register("doctor", "Examine the engine, the port, the disk, the keychain, and the presence check", runDoctor)
}

// Seams for the tests.
var (
	lcOwnerStore              = keychain.System
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

// setOwner keeps the owner key in the keychain and gives it to the server on stdin. The
// server keeps only a hash.
func (s *lcSession) setOwner(ctx context.Context, id string, m *manifest.Manifest) error {
	store := lcOwnerStore()
	account := keychain.OwnerAccount(s.cfg.Instance)
	key, err := store.Get(keychain.Service, account)
	made := false
	if errors.Is(err, keychain.ErrNotFound) {
		if key, err = lcNewOwnerKey(); err != nil {
			return err
		}
		if err := store.Set(keychain.Service, account, key); err != nil {
			return fmt.Errorf("keep the owner key in the keychain: %w", err)
		}
		made = true
	} else if err != nil {
		return fmt.Errorf("read the owner key from the keychain: %w", err)
	}
	if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: account}); err != nil {
		return err
	}
	code, err := s.eng.Exec(ctx, id, []string{"mai", "owner", "set-key"}, strings.NewReader(key+"\n"), io.Discard, lcErr)
	switch {
	case err != nil:
		return fmt.Errorf("mai owner set-key: %w", err)
	case code == 0:
		fmt.Fprintln(lcOut, "mai: the owner key is set")
	case code == lcOwnerKeyExists && made:
		fmt.Fprintln(lcErr, "mai: the server has a different owner key. To give it the key of the keychain, run: mai exec -- mai owner set-key --replace")
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
		if err := s.setOwner(ctx, id, m); err != nil {
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
		if key, err := lcOwnerStore().Get(keychain.Service, keychain.OwnerAccount(s.cfg.Instance)); err == nil {
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
	if err := presence.Check("mai exec runs a command as root in the mail server"); err != nil {
		return lcFail("the presence check failed: %v", err)
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
			return 0
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
		if err := s.setOwner(ctx, id, m); err != nil {
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
	return 0
}

// lcDoctorKeychainService is the keychain service of the round trip of doctor.
const lcDoctorKeychainService = "dev.mai.doctor"

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
	problems := 0
	ok := func(k, v string) { fmt.Fprintf(lcOut, "ok      %-9s %s\n", k, v) }
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
	if err := lcKeychainRoundTrip(keychain.System()); err != nil {
		bad("keychain", err.Error(), lcKeychainStep(g))
	} else {
		ok("keychain", "set, get, and delete of a test item succeeded")
	}
	switch err := presence.Check("mai doctor examines the presence check"); {
	case err == nil:
		ok("presence", "the presence check is available")
	case errors.Is(err, presence.ErrUnavailable):
		bad("presence", "this build has no presence check", "install a release build of mai")
	default:
		bad("presence", err.Error(), "agree to the prompt of the OS when mai shows it")
	}
	if problems > 0 {
		fmt.Fprintf(lcOut, "%d problem(s)\n", problems)
		return 1
	}
	fmt.Fprintln(lcOut, "no problems")
	return 0
}

func (s *lcSession) portIsMai(ctx context.Context) bool {
	c, ok, err := s.owned(ctx)
	return err == nil && ok && c.State != nil && c.State.Running
}

func lcKeychainRoundTrip(st keychain.Store) error {
	val, err := lcNewOwnerKey()
	if err != nil {
		return err
	}
	account := "doctor-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := st.Set(lcDoctorKeychainService, account, val); err != nil {
		return fmt.Errorf("set: %w", err)
	}
	got, err := st.Get(lcDoctorKeychainService, account)
	derr := st.Delete(lcDoctorKeychainService, account)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if got != val {
		return errors.New("get gave a different value")
	}
	if derr != nil {
		return fmt.Errorf("delete: %w", derr)
	}
	return nil
}

func lcKeychainStep(goos string) string {
	switch goos {
	case "linux":
		return "start a Secret Service (for example gnome-keyring), then run mai doctor again"
	case "windows":
		return "make sure that the Credential Manager service runs"
	default:
		return "unlock the login keychain, then run mai doctor again"
	}
}
