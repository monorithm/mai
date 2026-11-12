package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/internal/engine"
	"github.com/monorithm/mai/cli/internal/keychain"
	"github.com/monorithm/mai/cli/internal/manifest"
)

func TestOwnerKeyFormat(t *testing.T) {
	re := regexp.MustCompile(`^mai1:owner:[a-z2-7]{52}$`)
	a, err := lcNewOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := lcNewOwnerKey()
	if !re.MatchString(a) || a == b {
		t.Fatalf("bad owner key format, or two equal keys")
	}
}

func TestLegacyMessageNamesTheCommand(t *testing.T) {
	m := lcLegacyMessage("mai")
	if !strings.Contains(m, "mai uninstall --legacy") || !strings.Contains(m, "2.x") {
		t.Fatalf("message is %q", m)
	}
}

func TestKeychainRoundTripWithMemory(t *testing.T) {
	if err := lcKeychainRoundTrip(keychain.NewMemory()); err != nil {
		t.Fatal(err)
	}
}

// TestLifecycleWithEngine runs the lifecycle on a real engine. It runs only when
// MAI_IT_ENGINE is 1.
func TestLifecycleWithEngine(t *testing.T) {
	if os.Getenv("MAI_IT_ENGINE") != "1" {
		t.Skip("set MAI_IT_ENGINE=1 to run the lifecycle on a real engine")
	}
	const instance = "mai-wf-engine-it"
	home := t.TempDir()
	t.Setenv("MAI_HOME", home)
	var out, errs bytes.Buffer
	lcOut, lcErr = &out, &errs
	lcOwnerStore = func() keychain.Store { return keychain.NewMemory() }
	t.Cleanup(func() {
		ctx := context.Background()
		e, err := engine.ConnectSystem(ctx, &errs)
		if err != nil {
			t.Errorf("cleanup: %v", err)
			return
		}
		defer e.Close()
		if c, ok, _ := e.Container(ctx, instance); ok {
			if err := e.Remove(ctx, c.ID); err != nil {
				t.Errorf("cleanup: remove the container: %v", err)
			}
		}
		for _, v := range []string{engine.DataVolume(instance), engine.SpoolVolume(instance)} {
			if err := e.RemoveVolume(ctx, v); err != nil {
				t.Errorf("cleanup: remove the volume %s: %v", v, err)
			}
		}
		t.Log(out.String())
		t.Log(errs.String())
	})
	start := []string{"--instance", instance, "--port", "18765", "--image", "mai:dev", "--no-pull", "--skip-owner"}
	if code := runStart(start); code != 0 {
		t.Fatalf("start gave %d: %s", code, errs.String())
	}
	m, err := manifest.Load(filepath.Join(home, "state", manifest.FileName), instance)
	if err != nil {
		t.Fatal(err)
	}
	if c := m.Of(manifest.Container); len(c) != 1 || c[0].ID == "" || c[0].Name != instance {
		t.Fatalf("manifest containers are %+v", c)
	}
	if v := m.Of(manifest.Volume); len(v) != 2 {
		t.Fatalf("manifest volumes are %+v", v)
	}
	if len(m.Of(manifest.Image)) != 0 {
		t.Fatal("the manifest records an image that this run did not pull")
	}
	ctx := context.Background()
	e, err := engine.ConnectSystem(ctx, &errs)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	c, ok, err := e.Container(ctx, instance)
	if err != nil || !ok {
		t.Fatalf("no container: %v", err)
	}
	if engine.Owned(c) != nil || c.Config.Labels[engine.LabelInstance] != instance {
		t.Fatalf("labels are %v", c.Config.Labels)
	}
	for p, bs := range c.HostConfig.PortBindings {
		for _, b := range bs {
			if b.HostIP.String() != "127.0.0.1" || b.HostPort != "18765" {
				t.Fatalf("port %v has the binding %+v", p, b)
			}
		}
	}
	if code := runStatus(nil); code != 0 {
		t.Fatalf("status gave %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "healthy") {
		t.Fatalf("status shows no health: %s", out.String())
	}
	if code := runPause(nil); code != 0 {
		t.Fatalf("pause gave %d", code)
	}
	if c, _, _ := e.Container(ctx, instance); !c.State.Paused {
		t.Fatal("the container is not paused")
	}
	if code := runResume(nil); code != 0 {
		t.Fatalf("resume gave %d", code)
	}
	if c, _, _ := e.Container(ctx, instance); !c.State.Running || c.State.Paused {
		t.Fatal("the container does not run after the resume")
	}
	if code := runStop(nil); code != 0 {
		t.Fatalf("stop gave %d", code)
	}
	if c, _, _ := e.Container(ctx, instance); c.State.Running {
		t.Fatal("the container runs after the stop")
	}
}

// TestStartRefusesALegacyContainer makes a container with no label of the CLI and makes
// sure that start refuses it. It runs only when MAI_IT_ENGINE is 1.
func TestStartRefusesALegacyContainer(t *testing.T) {
	if os.Getenv("MAI_IT_ENGINE") != "1" {
		t.Skip("set MAI_IT_ENGINE=1 to run the lifecycle on a real engine")
	}
	const instance = "mai-wf-engine-legacy"
	t.Setenv("MAI_HOME", t.TempDir())
	var out, errs bytes.Buffer
	lcOut, lcErr = &out, &errs
	ctx := context.Background()
	e, err := engine.ConnectSystem(ctx, &errs)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	r, err := e.API.ContainerCreate(ctx, client.ContainerCreateOptions{Name: instance, Image: "mai:dev"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Remove(context.Background(), r.ID); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	code := runStart([]string{"--instance", instance, "--port", "18766", "--image", "mai:dev", "--no-pull", "--skip-owner"})
	if code == 0 {
		t.Fatal("start accepted a container with no label of the CLI")
	}
	if !strings.Contains(errs.String(), "mai uninstall --legacy") {
		t.Fatalf("the error does not name the command: %s", errs.String())
	}
}
