package engine

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

func TestCreateOptionsLabelsAndSecurity(t *testing.T) {
	o := Spec{Instance: "mai", Port: 8765, Image: "ghcr.io/monorithm/mai:3.0.0"}.CreateOptions()
	if o.Name != "mai" || o.Config.Image != "ghcr.io/monorithm/mai:3.0.0" {
		t.Fatalf("name or image is wrong: %q %q", o.Name, o.Config.Image)
	}
	if o.Config.Labels[LabelManagedBy] != "mai-cli" || o.Config.Labels[LabelInstance] != "mai" {
		t.Fatalf("labels are wrong: %v", o.Config.Labels)
	}
	if !slices.Equal(o.Config.Env, []string{"MAI_HOSTS=127.0.0.1:8765"}) {
		t.Fatalf("env is wrong: %v", o.Config.Env)
	}
	if o.Config.StopTimeout == nil || *o.Config.StopTimeout != 60 {
		t.Fatal("stop timeout is not 60")
	}
	h := o.HostConfig
	if !h.ReadonlyRootfs {
		t.Fatal("root is not read-only")
	}
	if !slices.Equal(h.CapDrop, []string{"ALL"}) {
		t.Fatalf("cap_drop is %v", h.CapDrop)
	}
	if !slices.Equal(h.CapAdd, []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID", "KILL"}) {
		t.Fatalf("cap_add is %v", h.CapAdd)
	}
	if h.RestartPolicy.Name != container.RestartPolicyUnlessStopped {
		t.Fatalf("restart policy is %q", h.RestartPolicy.Name)
	}
}

func TestCreateOptionsTmpfs(t *testing.T) {
	o := Spec{Instance: "mai", Port: 8765, Image: "i"}.CreateOptions()
	want := []string{"/run", "/tmp", "/etc/postfix", "/etc/dovecot", "/var/lib/postgresql"}
	if len(o.HostConfig.Tmpfs) != len(want) {
		t.Fatalf("tmpfs is %v", o.HostConfig.Tmpfs)
	}
	for _, p := range want {
		if _, ok := o.HostConfig.Tmpfs[p]; !ok {
			t.Fatalf("tmpfs has no %s", p)
		}
	}
}

func TestCreateOptionsMounts(t *testing.T) {
	o := Spec{Instance: "mai", Port: 8765, Image: "i"}.CreateOptions()
	m := o.HostConfig.Mounts
	if len(m) != 2 {
		t.Fatalf("mounts are %v", m)
	}
	if m[0].Type != mount.TypeVolume || m[0].Source != "mai3-data" || m[0].Target != "/data" {
		t.Fatalf("data mount is %+v", m[0])
	}
	if m[1].Type != mount.TypeVolume || m[1].Source != "mai3-spool" || m[1].Target != "/var/lib/mai/spool" {
		t.Fatalf("spool mount is %+v", m[1])
	}
	if len(o.HostConfig.Binds) != 0 {
		t.Fatalf("binds are %v", o.HostConfig.Binds)
	}
}

func TestCreateOptionsHostDataFolder(t *testing.T) {
	s := Spec{Instance: "x", Port: 1, Image: "i", DataDir: "/srv/mai"}
	o := s.CreateOptions()
	if o.HostConfig.Mounts[0].Type != mount.TypeBind || o.HostConfig.Mounts[0].Source != "/srv/mai" {
		t.Fatalf("data mount is %+v", o.HostConfig.Mounts[0])
	}
	if !slices.Equal(s.Volumes(), []string{"x3-spool"}) {
		t.Fatalf("volumes are %v", s.Volumes())
	}
}

func TestCreateOptionsPortOnLoopbackOnly(t *testing.T) {
	o := Spec{Instance: "mai", Port: 18765, Image: "i"}.CreateOptions()
	pm := o.HostConfig.PortBindings
	if len(pm) != 1 {
		t.Fatalf("port map is %v", pm)
	}
	for p, bs := range pm {
		if p.Num() != 8080 || string(p.Proto()) != "tcp" {
			t.Fatalf("container port is %v", p)
		}
		if len(bs) != 1 || bs[0].HostIP.String() != "127.0.0.1" || bs[0].HostPort != "18765" {
			t.Fatalf("bindings are %+v", bs)
		}
	}
	if len(o.Config.ExposedPorts) != 1 {
		t.Fatalf("exposed ports are %v", o.Config.ExposedPorts)
	}
	b, err := json.Marshal(o.HostConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"1993", "1587", "0.0.0.0"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("host config has %s: %s", bad, b)
		}
	}
}
