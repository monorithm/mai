package uninstall

import (
	"context"
	"errors"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

type fakeContainer struct {
	ID, Name, ImageID string
	Labels            map[string]string
}

type fakeImage struct {
	ID   string
	Tags []string
}

// fakeEngine keeps containers, volumes, and images in memory. It ignores the filters of a
// list, thus the code under test must select objects itself.
type fakeEngine struct {
	containers []fakeContainer
	volumes    map[string]map[string]string // name -> labels
	images     []fakeImage
	stuck      map[string]bool // names of objects that a remove does not remove
	removed    []string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{volumes: map[string]map[string]string{}, stuck: map[string]bool{}}
}

func (f *fakeEngine) findContainer(id string) int {
	for i, c := range f.containers {
		if c.ID == id || c.Name == id {
			return i
		}
	}
	return -1
}

func (f *fakeEngine) findImage(ref string) int {
	for i, im := range f.images {
		if im.ID == ref {
			return i
		}
		for _, t := range im.Tags {
			if t == ref {
				return i
			}
		}
	}
	return -1
}

func (f *fakeEngine) ContainerList(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
	var out client.ContainerListResult
	for _, c := range f.containers {
		out.Items = append(out.Items, container.Summary{ID: c.ID, Names: []string{"/" + c.Name}, ImageID: c.ImageID, Labels: c.Labels})
	}
	return out, nil
}

func (f *fakeEngine) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	i := f.findContainer(id)
	if i < 0 {
		return client.ContainerInspectResult{}, NotFoundError{What: id}
	}
	c := f.containers[i]
	var r client.ContainerInspectResult
	r.Container.ID = c.ID
	r.Container.Name = "/" + c.Name
	r.Container.Image = c.ImageID
	r.Container.Config = &container.Config{Labels: c.Labels}
	return r, nil
}

func (f *fakeEngine) ContainerRemove(_ context.Context, id string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	i := f.findContainer(id)
	if i < 0 {
		return client.ContainerRemoveResult{}, NotFoundError{What: id}
	}
	if !opts.RemoveVolumes {
		return client.ContainerRemoveResult{}, errors.New("test: RemoveVolumes must be true")
	}
	if f.stuck[f.containers[i].Name] {
		return client.ContainerRemoveResult{}, errors.New("test: the container is stuck")
	}
	f.removed = append(f.removed, "container "+f.containers[i].Name)
	f.containers = append(f.containers[:i], f.containers[i+1:]...)
	return client.ContainerRemoveResult{}, nil
}

func (f *fakeEngine) VolumeList(_ context.Context, _ client.VolumeListOptions) (client.VolumeListResult, error) {
	var out client.VolumeListResult
	for n, l := range f.volumes {
		out.Items = append(out.Items, volume.Volume{Name: n, Labels: l})
	}
	return out, nil
}

func (f *fakeEngine) VolumeInspect(_ context.Context, id string, _ client.VolumeInspectOptions) (client.VolumeInspectResult, error) {
	l, ok := f.volumes[id]
	if !ok {
		return client.VolumeInspectResult{}, NotFoundError{What: id}
	}
	return client.VolumeInspectResult{Volume: volume.Volume{Name: id, Labels: l}}, nil
}

func (f *fakeEngine) VolumeRemove(_ context.Context, id string, _ client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
	if _, ok := f.volumes[id]; !ok {
		return client.VolumeRemoveResult{}, NotFoundError{What: id}
	}
	if f.stuck[id] {
		return client.VolumeRemoveResult{}, errors.New("test: the volume is in use")
	}
	f.removed = append(f.removed, "volume "+id)
	delete(f.volumes, id)
	return client.VolumeRemoveResult{}, nil
}

func (f *fakeEngine) ImageList(_ context.Context, _ client.ImageListOptions) (client.ImageListResult, error) {
	var out client.ImageListResult
	for _, im := range f.images {
		out.Items = append(out.Items, image.Summary{ID: im.ID, RepoTags: im.Tags})
	}
	return out, nil
}

func (f *fakeEngine) ImageInspect(_ context.Context, id string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	i := f.findImage(id)
	if i < 0 {
		return client.ImageInspectResult{}, NotFoundError{What: id}
	}
	var r client.ImageInspectResult
	r.ID = f.images[i].ID
	r.RepoTags = f.images[i].Tags
	return r, nil
}

func (f *fakeEngine) ImageRemove(_ context.Context, id string, _ client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
	i := f.findImage(id)
	if i < 0 {
		return client.ImageRemoveResult{}, NotFoundError{What: id}
	}
	f.removed = append(f.removed, "image "+id)
	im := &f.images[i]
	if im.ID != id {
		var tags []string
		for _, t := range im.Tags {
			if t != id {
				tags = append(tags, t)
			}
		}
		im.Tags = tags
		if len(tags) > 0 {
			return client.ImageRemoveResult{}, nil
		}
	}
	f.images = append(f.images[:i], f.images[i+1:]...)
	return client.ImageRemoveResult{}, nil
}

func (f *fakeEngine) DiskUsage(_ context.Context, _ client.DiskUsageOptions) (client.DiskUsageResult, error) {
	var r client.DiskUsageResult
	for n := range f.volumes {
		r.Volumes.Items = append(r.Volumes.Items, volume.Volume{Name: n, UsageData: &volume.UsageData{Size: 2048}})
	}
	return r, nil
}

func (f *fakeEngine) ExecCreate(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
	return client.ExecCreateResult{}, errors.New("test: no exec")
}

func (f *fakeEngine) ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
	return client.ExecAttachResult{}, errors.New("test: no exec")
}

func (f *fakeEngine) ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
	return client.ExecInspectResult{}, errors.New("test: no exec")
}

func (f *fakeEngine) CopyFromContainer(context.Context, string, client.CopyFromContainerOptions) (client.CopyFromContainerResult, error) {
	return client.CopyFromContainerResult{}, errors.New("test: no copy")
}
