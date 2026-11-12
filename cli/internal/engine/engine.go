package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// ErrNotInstalled means that no engine is installed on this computer.
var ErrNotInstalled = errors.New("engine: no container engine is installed")

// ErrNoResponse means that the engine did not answer in the wait time.
var ErrNoResponse = errors.New("engine: the container engine gives no response")

// ErrForeign means that a container with the instance name exists without the label of
// the CLI.
var ErrForeign = errors.New("engine: a container that the CLI did not make holds the name")

// ErrPullRefused means that the image is not local and the caller refused a pull.
var ErrPullRefused = errors.New("engine: the image is not local and the pull is off")

// StartWait is the maximum time to wait for the engine or for the health of the container.
const StartWait = 120 * time.Second

// Engine is a connection to one container engine.
type Engine struct {
	API       *client.Client
	Candidate Candidate
}

func dial(ctx context.Context, c Candidate) (*Engine, error) {
	api, err := client.New(client.WithHost(c.Host), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := api.Ping(pctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		_ = api.Close()
		return nil, err
	}
	return &Engine{API: api, Candidate: c}, nil
}

// Find returns the first candidate that answers, or ErrNoResponse.
func Find(ctx context.Context, cands []Candidate) (*Engine, error) {
	for _, c := range cands {
		if e, err := dial(ctx, c); err == nil {
			return e, nil
		}
	}
	return nil, ErrNoResponse
}

// Launcher starts an engine that is installed but stopped.
type Launcher struct {
	Name string
	Cmd  []string
}

// Launchers returns the start commands of the installed engines, in sequence of
// preference. An empty list means that no engine is installed.
func Launchers(goos string) []Launcher {
	var out []Launcher
	look := func(name string) bool { _, err := exec.LookPath(name); return err == nil }
	switch goos {
	case "darwin":
		for _, app := range []string{"/Applications/Docker.app", filepath.Join(os.Getenv("HOME"), "Applications", "Docker.app")} {
			if _, err := os.Stat(app); err == nil {
				out = append(out, Launcher{Name: "Docker Desktop", Cmd: []string{"open", "-a", "Docker"}})
				break
			}
		}
	case "windows":
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		exe := filepath.Join(pf, "Docker", "Docker", "Docker Desktop.exe")
		if _, err := os.Stat(exe); err == nil {
			out = append(out, Launcher{Name: "Docker Desktop", Cmd: []string{exe}})
		}
	default:
		if look("dockerd") || look("docker") {
			out = append(out, Launcher{Name: "Docker Engine", Cmd: nil})
		}
	}
	if look("podman") {
		cmd := []string{"podman", "machine", "start"}
		if goos == "linux" {
			cmd = []string{"systemctl", "--user", "start", "podman.socket"}
		}
		out = append(out, Launcher{Name: "Podman", Cmd: cmd})
	}
	return out
}

// Connect finds the engine. If no engine answers, it starts the first installed engine and
// waits up to StartWait. If no engine is installed, it returns ErrNotInstalled.
func Connect(ctx context.Context, env Env, log io.Writer) (*Engine, error) {
	cands := Discover(env)
	if e, err := Find(ctx, cands); err == nil {
		return e, nil
	}
	ls := Launchers(env.GOOS)
	if len(ls) == 0 {
		return nil, ErrNotInstalled
	}
	l := ls[0]
	if l.Cmd == nil {
		return nil, fmt.Errorf("%w: start %s (for example: sudo systemctl start docker), then run the command again", ErrNoResponse, l.Name)
	}
	fmt.Fprintf(log, "mai: %s gives no response. Start it.\n", l.Name)
	cmd := exec.CommandContext(ctx, l.Cmd[0], l.Cmd[1:]...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("start %s: %w", l.Name, err)
	}
	deadline := time.Now().Add(StartWait)
	for time.Now().Before(deadline) {
		if e, err := Find(ctx, Discover(env)); err == nil {
			return e, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, ErrNoResponse
}

// ConnectSystem is Connect with the environment of this process.
func ConnectSystem(ctx context.Context, log io.Writer) (*Engine, error) {
	return Connect(ctx, SystemEnv(runtime.GOOS), log)
}

// Close ends the connection.
func (e *Engine) Close() error { return e.API.Close() }

// Find the container with the exact name. ok is false when no such container exists.
func (e *Engine) Container(ctx context.Context, name string) (container.InspectResponse, bool, error) {
	f := make(client.Filters).Add("name", "^/"+name+"$")
	r, err := e.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return container.InspectResponse{}, false, err
	}
	for _, s := range r.Items {
		for _, n := range s.Names {
			if n == "/"+name {
				in, err := e.API.ContainerInspect(ctx, s.ID, client.ContainerInspectOptions{})
				if err != nil {
					return container.InspectResponse{}, false, err
				}
				return in.Container, true, nil
			}
		}
	}
	return container.InspectResponse{}, false, nil
}

// Owned returns ErrForeign when the container has no label of the CLI.
func Owned(c container.InspectResponse) error {
	if c.Config == nil || c.Config.Labels[LabelManagedBy] != ManagedByValue {
		return ErrForeign
	}
	return nil
}

// LocalImage returns the ID of a local image with the reference, or "" when it is not
// local.
func (e *Engine) LocalImage(ctx context.Context, ref string) (string, error) {
	f := make(client.Filters).Add("reference", ref)
	r, err := e.API.ImageList(ctx, client.ImageListOptions{Filters: f})
	if err != nil {
		return "", err
	}
	for _, s := range r.Items {
		for _, t := range s.RepoTags {
			if t == ref || t == ref+":latest" {
				return s.ID, nil
			}
		}
	}
	return "", nil
}

// EnsureImage makes sure that the image is local. It pulls the image with no registry
// credential when it is not local. pulled is true when this call pulled the image.
func (e *Engine) EnsureImage(ctx context.Context, ref string, noPull bool, log io.Writer) (id string, pulled bool, err error) {
	id, err = e.LocalImage(ctx, ref)
	if err != nil || id != "" {
		return id, false, err
	}
	if noPull {
		return "", false, fmt.Errorf("%w: %s", ErrPullRefused, ref)
	}
	if err := e.Pull(ctx, ref, log); err != nil {
		return "", false, err
	}
	id, err = e.LocalImage(ctx, ref)
	if err == nil && id == "" {
		err = fmt.Errorf("engine: the pull of %s gave no local image", ref)
	}
	return id, err == nil, err
}

// Pull gets the image through the Engine API with no registry credential, and writes the
// progress to log.
func (e *Engine) Pull(ctx context.Context, ref string, log io.Writer) error {
	fmt.Fprintf(log, "mai: pull %s\n", ref)
	r, err := e.API.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	last := map[string]string{}
	for m, err := range r.JSONMessages(ctx) {
		if err != nil {
			return err
		}
		if m.Error != nil {
			return errors.New(m.Error.Message)
		}
		line := m.Status
		if m.Progress != nil && m.Progress.Total > 0 {
			line = fmt.Sprintf("%s %d%%", m.Status, m.Progress.Current*100/m.Progress.Total)
		}
		if last[m.ID] == line {
			continue
		}
		last[m.ID] = line
		if m.ID != "" {
			fmt.Fprintf(log, "  %s: %s\n", m.ID, line)
		} else if line != "" {
			fmt.Fprintf(log, "  %s\n", line)
		}
	}
	return nil
}

// EnsureVolumes makes each volume of the spec with the labels of the CLI. made is the list
// of volumes that this call made.
func (e *Engine) EnsureVolumes(ctx context.Context, s Spec) (made []string, err error) {
	for _, v := range s.Volumes() {
		f := make(client.Filters).Add("name", v)
		r, err := e.API.VolumeList(ctx, client.VolumeListOptions{Filters: f})
		if err != nil {
			return made, err
		}
		found := false
		for _, x := range r.Items {
			if x.Name == v {
				found = true
			}
		}
		if found {
			continue
		}
		if _, err := e.API.VolumeCreate(ctx, client.VolumeCreateOptions{Name: v, Labels: Labels(s.Instance)}); err != nil {
			return made, err
		}
		made = append(made, v)
	}
	return made, nil
}

// Create makes the container of the spec and returns its ID.
func (e *Engine) Create(ctx context.Context, s Spec) (string, error) {
	r, err := e.API.ContainerCreate(ctx, s.CreateOptions())
	if err != nil {
		return "", err
	}
	return r.ID, nil
}

// Start starts the container.
func (e *Engine) Start(ctx context.Context, id string) error {
	_, err := e.API.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// Stop stops the container with the stop timeout of the contract.
func (e *Engine) Stop(ctx context.Context, id string) error {
	t := StopTimeout
	_, err := e.API.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &t})
	return err
}

// Pause freezes the container.
func (e *Engine) Pause(ctx context.Context, id string) error {
	_, err := e.API.ContainerPause(ctx, id, client.ContainerPauseOptions{})
	return err
}

// Resume unfreezes the container.
func (e *Engine) Resume(ctx context.Context, id string) error {
	_, err := e.API.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{})
	return err
}

// Remove removes the container with its anonymous volumes.
func (e *Engine) Remove(ctx context.Context, id string) error {
	_, err := e.API.ContainerRemove(ctx, id, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true})
	return err
}

// RemoveVolume removes one volume.
func (e *Engine) RemoveVolume(ctx context.Context, name string) error {
	_, err := e.API.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	return err
}

// Exec runs cmd in the container and returns its exit code. stdin can be nil.
func (e *Engine) Exec(ctx context.Context, id string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	cr, err := e.API.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd: cmd, AttachStdin: stdin != nil, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return -1, err
	}
	at, err := e.API.ExecAttach(ctx, cr.ID, client.ExecAttachOptions{})
	if err != nil {
		return -1, err
	}
	defer at.Close()
	if stdin != nil {
		go func() {
			_, _ = io.Copy(at.Conn, stdin)
			_ = at.CloseWrite()
		}()
	}
	if _, err := stdcopy.StdCopy(stdout, stderr, at.Reader); err != nil && !errors.Is(err, io.EOF) {
		return -1, err
	}
	for {
		in, err := e.API.ExecInspect(ctx, cr.ID, client.ExecInspectOptions{})
		if err != nil {
			return -1, err
		}
		if !in.Running {
			return in.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Healthy runs "mai health" in the container once and returns nil on exit 0.
func (e *Engine) Healthy(ctx context.Context, id string, out io.Writer) error {
	code, err := e.Exec(ctx, id, []string{"mai", "health"}, nil, out, out)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("mai health gave exit code %d", code)
	}
	return nil
}

// WaitHealthy runs "mai health" in the container until it gives exit 0, up to wait.
func (e *Engine) WaitHealthy(ctx context.Context, id string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	var last error
	for time.Now().Before(deadline) {
		if last = e.Healthy(ctx, id, nil); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("the container is not healthy after %s: %v", wait, last)
}

// Logs writes the log of the container to stdout and stderr.
func (e *Engine) Logs(ctx context.Context, id string, follow bool, stdout, stderr io.Writer) error {
	r, err := e.API.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: follow})
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = stdcopy.StdCopy(stdout, stderr, r)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Describe returns one line about the engine: its platform and version.
func (e *Engine) Describe(ctx context.Context) string {
	v, err := e.API.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return e.Candidate.Source + " at " + e.Candidate.Host
	}
	name := strings.TrimSpace(v.Platform.Name)
	if name == "" {
		name = string(e.Candidate.Kind)
	}
	return fmt.Sprintf("%s %s (API %s) at %s", name, v.Version, v.APIVersion, e.Candidate.Host)
}
