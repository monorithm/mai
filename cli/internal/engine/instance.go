package engine

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// HostPort gives the host port of the HTTP port of the container, or 0 when the container has
// no binding of that port.
func HostPort(c container.InspectResponse) int {
	if c.HostConfig == nil {
		return 0
	}
	port := network.MustParsePort(strconv.Itoa(ContainerPort) + "/tcp")
	for _, b := range c.HostConfig.PortBindings[port] {
		if p, err := strconv.Atoi(b.HostPort); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

// DataDir gives the host folder of the bind mount at /data of the container. It gives "" when
// /data is a volume, or when the container has no mount at /data.
func DataDir(c container.InspectResponse) string {
	for _, m := range c.Mounts {
		if m.Destination == "/data" {
			if m.Type == mount.TypeBind {
				return m.Source
			}
			return ""
		}
	}
	if c.HostConfig != nil {
		for _, m := range c.HostConfig.Mounts {
			if m.Target == "/data" {
				if m.Type == mount.TypeBind {
					return m.Source
				}
				return ""
			}
		}
	}
	return ""
}

// Mount is a bind mount of a container that has the label of an instance.
type Mount struct {
	Instance  string // The instance of the container.
	Container string // The name of the container.
	Source    string // The host folder.
	Target    string // The folder in the container.
}

// BindMounts gives each bind mount of each container that has the label of an instance. The
// list of the containers can give a different path for the host folder, for example
// /host_mnt/private/tmp/x on Docker Desktop. Thus the inspect of each container gives the path.
func (e *Engine) BindMounts(ctx context.Context) ([]Mount, error) {
	f := make(client.Filters).Add("label", LabelInstance)
	r, err := e.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	var out []Mount
	for _, s := range r.Items {
		in, err := e.API.ContainerInspect(ctx, s.ID, client.ContainerInspectOptions{})
		if err != nil {
			return nil, err
		}
		out = append(out, HostMounts(in.Container)...)
	}
	return out, nil
}

// HostMounts gives each bind mount of the container, when the container has the label of an
// instance.
func HostMounts(c container.InspectResponse) []Mount {
	if c.Config == nil {
		return nil
	}
	inst, ok := c.Config.Labels[LabelInstance]
	if !ok {
		return nil
	}
	name := strings.TrimPrefix(c.Name, "/")
	var out []Mount
	for _, m := range c.Mounts {
		if m.Type == mount.TypeBind && m.Source != "" {
			out = append(out, Mount{Instance: inst, Container: name, Source: m.Source, Target: m.Destination})
		}
	}
	if c.HostConfig != nil {
		for _, m := range c.HostConfig.Mounts {
			if m.Type == mount.TypeBind && m.Source != "" {
				out = append(out, Mount{Instance: inst, Container: name, Source: m.Source, Target: m.Target})
			}
		}
	}
	return out
}

// Overlap tells if the folders a and b are the same folder, or if one of them holds the other.
func Overlap(a, b string) bool {
	a, b = realPath(a), realPath(b)
	return within(a, b) || within(b, a)
}

// realPath gives the clean absolute path, with the symbolic links of the part that exists
// resolved. On macOS, /var is a link to /private/var, and an engine can give either path.
func realPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// within tells if path is root or is in the folder root.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
