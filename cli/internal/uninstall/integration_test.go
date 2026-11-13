package uninstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

const (
	itInstance = "mai-wf-uninstall-it"
	itImageTag = "mai:wf-uninstall"
)

func itEngine(t *testing.T) *client.Client {
	t.Helper()
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if os.Getenv("DOCKER_HOST") == "" {
		home, _ := os.UserHomeDir()
		s := filepath.Join(home, ".docker", "run", "docker.sock")
		if _, err := os.Stat(s); err == nil {
			opts = append(opts, client.WithHost("unix://"+s))
		}
	}
	c, err := client.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestIntegrationUninstall makes its own container, volumes, image tag, manifest, key file,
// and legacy item in a keychain in memory, then uninstalls that instance and does a check.
// Set MAI_IT_UNINSTALL=1.
func TestIntegrationUninstall(t *testing.T) {
	if os.Getenv("MAI_IT_UNINSTALL") != "1" {
		t.Skip("set MAI_IT_UNINSTALL=1 to run the test with the engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	eng := itEngine(t)
	defer eng.Close()
	kc := keychain.NewMemory()
	agent := keys.AgentAccount(itInstance, "it")
	t.Cleanup(func() {
		_, _ = eng.ContainerRemove(context.Background(), itInstance, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		for _, v := range []string{DataVolume(itInstance), SpoolVolume(itInstance)} {
			_, _ = eng.VolumeRemove(context.Background(), v, client.VolumeRemoveOptions{Force: true})
		}
	})

	labels := map[string]string{LabelManagedBy: ManagedByValue, LabelInstance: itInstance}
	for _, v := range []string{DataVolume(itInstance), SpoolVolume(itInstance)} {
		if _, err := eng.VolumeCreate(ctx, client.VolumeCreateOptions{Name: v, Labels: labels}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := eng.ImageTag(ctx, client.ImageTagOptions{Source: "mai:dev", Target: itImageTag}); err != nil {
		t.Fatal(err)
	}
	cr, err := eng.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: itInstance,
		Config: &container.Config{
			Image:      itImageTag,
			Labels:     labels,
			Entrypoint: []string{"sleep"},
			Cmd:        []string{"300"},
		},
		HostConfig: &container.HostConfig{
			Binds: []string{DataVolume(itInstance) + ":/data", SpoolVolume(itInstance) + ":/var/lib/mai/spool"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	kc.Put(agent, "mai1:it:legacy-test-only")

	home := t.TempDir()
	t.Setenv("MAI_HOME", home)
	dirs, _ := paths.Default()
	kf := keys.Open(dirs.State, kc)
	if err := kf.Set(keys.OwnerAccount(itInstance), "mai1:owner:test-only"); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(dirs.Config, 0o700)
	_ = os.WriteFile(filepath.Join(dirs.Config, ConfigFile), []byte(`{"instance":"`+itInstance+`","port":18785}`), 0o600)
	man, _ := manifest.Load(filepath.Join(dirs.State, manifest.FileName), itInstance)
	for _, it := range []manifest.Item{
		{Type: manifest.Container, Name: itInstance, ID: cr.ID},
		{Type: manifest.Volume, Name: DataVolume(itInstance)},
		{Type: manifest.Volume, Name: SpoolVolume(itInstance)},
		{Type: manifest.Image, Name: itImageTag},
		{Type: manifest.KeychainKey, Name: agent},
	} {
		if err := man.Add(it); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	u := &Uninstaller{
		Engine:  eng,
		Keys:    kf,
		Dirs:    dirs,
		Yes:     true,
		Out:     &out,
		BaseURL: "http://127.0.0.1:18785",
	}
	code := u.Run(ctx)
	t.Log("\n" + out.String())
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := eng.ContainerInspect(ctx, itInstance, client.ContainerInspectOptions{}); !IsNotFound(err) {
		t.Errorf("the container exists: %v", err)
	}
	for _, v := range []string{DataVolume(itInstance), SpoolVolume(itInstance)} {
		if _, err := eng.VolumeInspect(ctx, v, client.VolumeInspectOptions{}); !IsNotFound(err) {
			t.Errorf("the volume %s exists: %v", v, err)
		}
	}
	if _, err := eng.ImageInspect(ctx, "mai:dev"); err != nil {
		t.Errorf("the image mai:dev is gone: %v", err)
	}
	if strings.Contains(out.String(), "gone     image") {
		if _, err := eng.ImageInspect(ctx, itImageTag); !IsNotFound(err) {
			t.Errorf("the tag %s exists: %v", itImageTag, err)
		}
	}
	if _, err := kc.Get(agent); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("the legacy keychain item exists: %v", err)
	}
	if !strings.Contains(out.String(), "gone     key owner@"+itInstance) {
		t.Error("the uninstall did not remove the owner key from the key file")
	}
	if _, err := os.Stat(dirs.State); !os.IsNotExist(err) {
		t.Error("the state folder exists")
	}
}
