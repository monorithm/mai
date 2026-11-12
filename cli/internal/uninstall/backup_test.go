package uninstall

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/moby/moby/client"
)

// backupEngine answers the exec of "mai backup" and the copy of the two files.
type backupEngine struct {
	*fakeEngine
	cmd   []string
	files map[string]string // path in the container -> content
	exit  int
}

func (b *backupEngine) ExecCreate(_ context.Context, _ string, o client.ExecCreateOptions) (client.ExecCreateResult, error) {
	b.cmd = o.Cmd
	return client.ExecCreateResult{ID: "e1"}, nil
}

func (b *backupEngine) ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
	var buf bytes.Buffer
	writeFrame(&buf, []byte("{\n  \"stamp\": \"20261112T172800Z\",\n  \"dump\": \"/data/backup/mai-1.pgdump\",\n  \"mail\": \"/data/backup/mai-1.mail.tar.gz\",\n  \"removed\": []\n}\n"))
	var r client.ExecAttachResult
	r.Reader = bufio.NewReader(&buf)
	return r, nil
}

// writeFrame writes one frame of the multiplexed stream of the engine, for stdout.
func writeFrame(w *bytes.Buffer, p []byte) {
	h := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(h[4:], uint32(len(p)))
	w.Write(h)
	w.Write(p)
}

func (b *backupEngine) ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
	return client.ExecInspectResult{ExitCode: b.exit}, nil
}

func (b *backupEngine) CopyFromContainer(_ context.Context, _ string, o client.CopyFromContainerOptions) (client.CopyFromContainerResult, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := b.files[o.SourcePath]
	_ = tw.WriteHeader(&tar.Header{Name: path.Base(o.SourcePath), Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	return client.CopyFromContainerResult{Content: io.NopCloser(&buf)}, nil
}

func TestWriteBackupMakesOneArchive(t *testing.T) {
	e := &backupEngine{fakeEngine: newFakeEngine(), files: map[string]string{
		"/data/backup/mai-1.pgdump":      "dump",
		"/data/backup/mai-1.mail.tar.gz": "mail",
	}}
	dest := filepath.Join(t.TempDir(), "mai-backup.tar")
	if err := WriteBackup(context.Background(), e, "mai", dest); err != nil {
		t.Fatal(err)
	}
	if len(e.cmd) != 2 || e.cmd[0] != "mai" || e.cmd[1] != "backup" {
		t.Errorf("command %v", e.cmd)
	}
	f, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := map[string]string{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = string(b)
	}
	if got["mai-1.pgdump"] != "dump" || got["mai-1.mail.tar.gz"] != "mail" || len(got) != 2 {
		t.Errorf("archive %v", got)
	}
	if fi, _ := os.Stat(dest); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

func TestWriteBackupFailsOnAnExitCode(t *testing.T) {
	e := &backupEngine{fakeEngine: newFakeEngine(), exit: 1}
	dest := filepath.Join(t.TempDir(), "mai-backup.tar")
	if err := WriteBackup(context.Background(), e, "mai", dest); err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a failed backup left a file")
	}
}

func TestUninstallWritesTheBackupBeforeTheRemove(t *testing.T) {
	e := setup(t)
	be := &backupEngine{fakeEngine: e.eng, files: map[string]string{
		"/data/backup/mai-1.pgdump":      "dump",
		"/data/backup/mai-1.mail.tar.gz": "mail",
	}}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Engine = be
	u.Backup = filepath.Join(t.TempDir(), "keep.tar")
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if _, err := os.Stat(u.Backup); err != nil {
		t.Fatalf("no backup: %v", err)
	}
}
