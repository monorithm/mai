package uninstall

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// backupAnswer is the JSON that "mai backup" writes in the container.
type backupAnswer struct {
	Dump string `json:"dump"`
	Mail string `json:"mail"`
}

// ExecOutput runs a command in the container and gives its standard output and exit code.
func ExecOutput(ctx context.Context, e Engine, container string, cmd []string) ([]byte, []byte, int, error) {
	ex, err := e.ExecCreate(ctx, container, client.ExecCreateOptions{Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return nil, nil, -1, err
	}
	att, err := e.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, nil, -1, err
	}
	var stdout, stderr bytes.Buffer
	if att.Reader != nil {
		_, err = stdcopy.StdCopy(&stdout, &stderr, att.Reader)
	}
	if att.Conn != nil {
		att.Close()
	}
	if err != nil {
		return nil, nil, -1, err
	}
	in, err := e.ExecInspect(ctx, ex.ID, client.ExecInspectOptions{})
	if err != nil {
		return nil, nil, -1, err
	}
	return stdout.Bytes(), stderr.Bytes(), in.ExitCode, nil
}

func parseBackupAnswer(out []byte) (backupAnswer, error) {
	var a backupAnswer
	if i := bytes.IndexByte(out, '{'); i >= 0 {
		out = out[i:]
	}
	if err := json.Unmarshal(out, &a); err != nil {
		return a, fmt.Errorf("the answer of mai backup is not valid: %w", err)
	}
	if a.Dump == "" || a.Mail == "" {
		return a, errors.New("the answer of mai backup has no file names")
	}
	return a, nil
}

// WriteBackup runs "mai backup" in the container and writes one tar archive at dest. The
// archive holds the database dump and the mail tar of the backup.
func WriteBackup(ctx context.Context, e Engine, container, dest string) error {
	stdout, stderr, code, err := ExecOutput(ctx, e, container, []string{"mai", "backup"})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("mai backup in the container exited with %d: %s", code, bytes.TrimSpace(stderr))
	}
	ans, err := parseBackupAnswer(stdout)
	if err != nil {
		return err
	}
	tmp := dest + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	werr := func() error {
		for _, src := range []string{ans.Dump, ans.Mail} {
			if err := copyEntries(ctx, e, container, src, tw); err != nil {
				return err
			}
		}
		return tw.Close()
	}()
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return werr
	}
	return os.Rename(tmp, dest)
}

func copyEntries(ctx context.Context, e Engine, container, src string, tw *tar.Writer) error {
	res, err := e.CopyFromContainer(ctx, container, client.CopyFromContainerOptions{SourcePath: src})
	if err != nil {
		return fmt.Errorf("copy %s from the container: %w", src, err)
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	n := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		h.Name = path.Base(h.Name)
		h.Mode = 0o600
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return fmt.Errorf("the copy of %s from the container holds no file", src)
	}
	return nil
}
