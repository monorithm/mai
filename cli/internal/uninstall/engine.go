// Package uninstall removes each item that the CLI made: the container, the volumes, the
// images, the entries in the files of the apps, the keychain items, the folders of the CLI,
// and at the end the binary. It also removes a 2.x install on request.
//
// The functions take an engine interface and folder paths, so that the tests use fakes.
package uninstall

import (
	"context"
	"errors"
	"strings"

	"github.com/moby/moby/client"
)

// Labels of the objects that the CLI makes.
const (
	LabelManagedBy = "dev.mai.managed-by"
	ManagedByValue = "mai-cli"
	LabelInstance  = "dev.mai.instance"
	LabelCompose   = "com.docker.compose.project"
)

// Engine is the part of the Engine API that the uninstall uses. *client.Client satisfies it.
type Engine interface {
	ContainerList(ctx context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(ctx context.Context, id string, opts client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerRemove(ctx context.Context, id string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	VolumeList(ctx context.Context, opts client.VolumeListOptions) (client.VolumeListResult, error)
	VolumeInspect(ctx context.Context, id string, opts client.VolumeInspectOptions) (client.VolumeInspectResult, error)
	VolumeRemove(ctx context.Context, id string, opts client.VolumeRemoveOptions) (client.VolumeRemoveResult, error)
	ImageList(ctx context.Context, opts client.ImageListOptions) (client.ImageListResult, error)
	ImageInspect(ctx context.Context, id string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageRemove(ctx context.Context, id string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	DiskUsage(ctx context.Context, opts client.DiskUsageOptions) (client.DiskUsageResult, error)
	ExecCreate(ctx context.Context, id string, opts client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, id string, opts client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(ctx context.Context, id string, opts client.ExecInspectOptions) (client.ExecInspectResult, error)
	CopyFromContainer(ctx context.Context, id string, opts client.CopyFromContainerOptions) (client.CopyFromContainerResult, error)
}

// IsNotFound tells if an engine error means that the object does not exist. The client
// keeps the error class out of reach without a further module, thus the text of the answer
// of the engine is also examined.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var nf interface{ NotFound() }
	if errors.As(err, &nf) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, m := range []string{"no such container", "no such volume", "no such image", "image not known", "volume not found"} {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// NotFoundError is an engine error for a missing object. The fake engine of the tests uses it.
type NotFoundError struct{ What string }

func (e NotFoundError) Error() string { return "not found: " + e.What }

// NotFound marks the error as a missing object.
func (NotFoundError) NotFound() {}

// hasName tells if a container summary has the name. The engine gives names with a slash.
func hasName(names []string, name string) bool {
	for _, n := range names {
		if n == "/"+name || n == name {
			return true
		}
	}
	return false
}
