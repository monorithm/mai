package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

func TestOverlapFindsTheSameFolderAndAFolderInIt(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	link := filepath.Join(root, "link")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(data, link); err != nil {
		t.Skipf("no symbolic link: %v", err)
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{data, data, true},
		{data, filepath.Join(data, "pg"), true},
		{filepath.Join(data, "pg"), data, true},
		{link, data, true},
		{filepath.Join(link, "new"), data, true},
		{data, data + "2", false},
		{data, filepath.Join(root, "other"), false},
	} {
		if got := Overlap(tc.a, tc.b); got != tc.want {
			t.Errorf("Overlap(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestDataDirGivesOnlyTheHostFolderOfABindMountAtData(t *testing.T) {
	bind := container.InspectResponse{Mounts: []container.MountPoint{{Type: mount.TypeBind, Source: "/host/data", Destination: "/data"}}}
	vol := container.InspectResponse{Mounts: []container.MountPoint{{Type: mount.TypeVolume, Name: "mai3-data", Source: "/var/lib/docker/volumes/mai3-data/_data", Destination: "/data"}}}
	if got := DataDir(bind); got != "/host/data" {
		t.Errorf("DataDir of a bind mount = %q", got)
	}
	if got := DataDir(vol); got != "" {
		t.Errorf("DataDir of a volume = %q", got)
	}
	if got := HostPort(container.InspectResponse{}); got != 0 {
		t.Errorf("HostPort of a container with no binding = %d", got)
	}
}
