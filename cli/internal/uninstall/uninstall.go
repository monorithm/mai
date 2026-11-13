package uninstall

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// RemoveAppEntry removes one entry that mai connect wrote in a file of an app. The part of the
// CLI that writes the entries sets it. When it is nil, the uninstall keeps each entry and
// shows a warning.
var RemoveAppEntry func(manifest.Item) error

// RemoveCreatedAppFile removes a file of an app that mai connect made, only when the file holds
// nothing of the user after the remove of the entries. The result is false for a file that
// stays. The part of the CLI that writes app entries sets it; nil keeps each such file.
var RemoveCreatedAppFile func(manifest.Item) (bool, error)

// IsKept reports if an error of RemoveAppEntry means that the entry stays, because other
// content of the file needs it. nil counts each error as a failure.
var IsKept func(error) bool

// States of a result.
const (
	Gone    = "gone"
	Failure = "failure"
	Kept    = "kept"
	Skipped = "skipped"
)

// Result is the state of one item after the remove.
type Result struct {
	Item   string
	State  string
	Detail string
}

// Failed tells if one result is a failure.
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.State == Failure {
			return true
		}
	}
	return false
}

// Uninstaller removes one instance and the files of the CLI. The files of the CLI that the
// other instances of the same folders use stay: the key file, the manifest, the configuration
// file of a different instance, and the binary.
type Uninstaller struct {
	Engine   Engine
	Keys     *keys.File // the key file, with the legacy keychain; nil = no key is removed
	Dirs     paths.Dirs
	Instance string // empty = the instance of config.json
	Yes      bool
	Backup   string // path of a backup archive; empty = no backup
	In       io.Reader
	Out      io.Writer
	HTTP     *http.Client
	BaseURL  string  // base URL of the server; empty = the host port of the container of the instance
	Binary   *Binary // nil = keep the binary
}

// Plan is the list of items that the uninstall removes.
type Plan struct {
	Instance   string
	Containers []string
	Volumes    []string
	Images     []manifest.Item
	AppEntries []manifest.Item
	AppFiles   []manifest.Item // files of apps that mai connect made
	// KeptEntries and KeptFiles hold the app entries and the files of apps of the instance that
	// stay, because a different instance has keys or records. An entry names no instance, thus
	// it serves the instance of config.json at each start of its bridge. Their records get the
	// instance EntryOwner, thus the uninstall of the last instance removes them.
	KeptEntries []manifest.Item
	KeptFiles   []manifest.Item
	EntryOwner  string
	Keys        []string // accounts of the instance in the key file
	Legacy      []string // accounts of the instance with a legacy keychain item
	LegacyErr   error    // the error of the examination of the legacy keychain
	Paths       []string
	DataDirs    []string // the data folders of the instance in Paths
	// Unowned holds each data folder of a record of mai 3.0 or 3.1 whose instance is not
	// known. It stays.
	Unowned []string
	// keptData holds each data folder that stays: a folder of Unowned, and each data folder of
	// a different instance. The remove of a folder of the CLI that holds such a folder keeps
	// it.
	keptData []keptPath
	// mounted holds each folder of the instance that a container of a different instance
	// mounts. It stays, and it is not in Paths.
	mounted []keptFolder
	// shared holds each record of a file of an app of mai 3.0 or 3.1. It has no instance.
	shared []manifest.Item
	// keptImages holds the record of each image of Images that stays, because a container of
	// a different instance of this state folder uses it. Remove sets it. Each record has the
	// instance of that container, thus the uninstall of that instance removes the image.
	keptImages []manifest.Item
	Manifest   *manifest.Manifest
	Config     Config
	// Others holds each different instance with a key in the key file or a record in the
	// manifest, sorted. MakePlan sets it, and Remove sets it again after the remove. While
	// it is not empty, the files of the CLI that the other instances use stay.
	Others []string
}

func (u *Uninstaller) out() io.Writer {
	if u.Out == nil {
		return io.Discard
	}
	return u.Out
}

func appendNew(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// MakePlan reads config.json and the manifest and gives the items to remove. The plan holds
// only the items of the instance. The records of a different instance stay out of the plan.
func (u *Uninstaller) MakePlan() (*Plan, error) {
	cfg, err := LoadConfig(u.Dirs.Config)
	if err != nil {
		return nil, fmt.Errorf("read the configuration: %w", err)
	}
	inst := u.Instance
	if inst == "" {
		inst = cfg.Instance
	}
	man, err := manifest.Load(filepath.Join(u.Dirs.State, manifest.FileName), inst)
	if err != nil {
		return nil, fmt.Errorf("read the manifest: %w", err)
	}
	p := &Plan{Instance: inst, Manifest: man, Config: cfg}
	p.Containers = appendNew(p.Containers, inst)
	for _, it := range man.OfInstance(inst, manifest.Container) {
		p.Containers = appendNew(p.Containers, it.Name)
	}
	p.Volumes = appendNew(p.Volumes, DataVolume(inst))
	p.Volumes = appendNew(p.Volumes, SpoolVolume(inst))
	for _, it := range man.OfInstance(inst, manifest.Volume) {
		p.Volumes = appendNew(p.Volumes, it.Name)
	}
	p.Images = man.OfInstance(inst, manifest.Image)
	p.AppEntries = man.OfInstance(inst, manifest.AppEntry)
	// A record of mai 3.0 or 3.1 of an app entry or a file of an app has no instance, because the
	// entry names no instance. Each uninstall keeps it while a different instance stays, and the
	// uninstall of the last instance removes it.
	for _, it := range man.Unassigned() {
		switch it.Type {
		case manifest.AppEntry:
			p.AppEntries = append(p.AppEntries, it)
		case manifest.File:
			p.shared = append(p.shared, it)
		}
	}
	// A "version" entry goes after the other entries of its file, because a hook that stays
	// needs it.
	sort.SliceStable(p.AppEntries, func(i, j int) bool {
		return p.AppEntries[i].Pointer != "/version" && p.AppEntries[j].Pointer == "/version"
	})
	if u.Keys != nil {
		inFile, err := u.Keys.OfInstance(inst)
		if err != nil {
			return nil, fmt.Errorf("read the key file: %w", err)
		}
		p.Keys = appendNew(p.Keys, keys.OwnerAccount(inst))
		for _, a := range inFile {
			p.Keys = appendNew(p.Keys, a)
		}
		var recorded []string
		for _, it := range man.Of(manifest.KeychainKey) {
			recorded = append(recorded, it.Name)
		}
		candidates, err := u.Keys.Candidates(inst, recorded)
		if err != nil {
			return nil, fmt.Errorf("read the key file: %w", err)
		}
		p.Legacy, p.LegacyErr = u.Keys.LegacyItems(candidates)
		switch {
		case errors.Is(p.LegacyErr, keychain.ErrUnavailable):
			// mai 3.0 and 3.1 did not operate with no keychain. Thus only a recorded item can
			// exist. The remove cannot delete it, and tells about it.
			for _, a := range recorded {
				if keys.InstanceOf(a) == inst {
					p.Legacy = appendNew(p.Legacy, a)
				}
			}
		case p.LegacyErr != nil:
			// The examination failed, thus each candidate can have an item. The remove
			// deletes the item of each candidate and does a check of it.
			for _, a := range candidates {
				p.Legacy = appendNew(p.Legacy, a)
			}
		}
	}
	// A file of an app can hold content of the user after mai connect made it, thus it does
	// not go with the folders of mai.
	p.AppFiles = append(man.OfInstance(inst, manifest.File), p.shared...)
	// The record of a data folder of mai 3.0 or 3.1 has no instance. The mounts of the
	// containers and config.json tell its instance.
	mounts := u.mounts(context.Background())
	for _, it := range man.Unassigned() {
		if it.Type != manifest.Folder || it.Name != manifest.DataFolder {
			continue
		}
		switch owner := dataOwner(it.Path, cfg, mounts); owner {
		case inst:
			man.Assign(it, inst)
		case "":
			p.Unowned = appendNew(p.Unowned, it.Path)
			p.keptData = append(p.keptData, keptPath{it.Path, "the data folder " + it.Path + " of mai 3.0 or 3.1, whose instance is not known"})
		default:
			p.keptData = append(p.keptData, keptPath{it.Path, dataText(it.Path, owner)})
		}
	}
	// A data folder of a different instance stays, also when no container mounts it at this
	// time, for example after a start that did not make the container.
	for _, it := range man.Of(manifest.Folder) {
		if it.Name == manifest.DataFolder && it.Instance != "" && it.Instance != inst {
			p.keptData = append(p.keptData, keptPath{it.Path, dataText(it.Path, it.Instance)})
		}
	}
	for _, it := range man.OfInstance(inst, manifest.Folder) {
		p.Paths = appendNew(p.Paths, it.Path)
		if it.Name == manifest.DataFolder {
			p.DataDirs = appendNew(p.DataDirs, it.Path)
		}
	}
	p.Paths = appendNew(p.Paths, u.Dirs.Cache)
	p.Paths = appendNew(p.Paths, u.Dirs.State)
	p.Paths = appendNew(p.Paths, u.Dirs.Config)
	// A folder that a container of a different instance mounts stays, also when a record names
	// this instance. Thus the plan does not list it, and the size does not count it.
	var others []engine.Mount
	for _, m := range mounts {
		if m.Instance != inst {
			others = append(others, m)
		}
	}
	var paths, data []string
	for _, f := range p.Paths {
		if m, ok := mountHolds(others, f); ok {
			p.mounted = append(p.mounted, keptFolder{f, mountedText(m)})
			continue
		}
		paths = append(paths, f)
		if holds(p.DataDirs, f) {
			data = append(data, f)
		}
	}
	p.Paths, p.DataDirs = paths, data
	var keyOthers []string
	if u.Keys != nil {
		all, err := u.Keys.Accounts()
		if err != nil {
			return nil, fmt.Errorf("read the key file: %w", err)
		}
		keyOthers = instancesExcept(accountInstances(all), inst)
	}
	p.Others = union(keyOthers, instancesExcept(man.Instances(), inst))
	if len(p.Others) > 0 && len(p.AppEntries)+len(p.AppFiles) > 0 {
		p.KeptEntries, p.KeptFiles, p.AppEntries, p.AppFiles = p.AppEntries, p.AppFiles, nil, nil
		p.EntryOwner = p.Others[0]
		if holds(p.Others, cfg.Instance) {
			p.EntryOwner = cfg.Instance
		}
	}
	return p, nil
}

// dataText tells that a folder is the data folder of an instance.
func dataText(dir, instance string) string {
	return "the data folder " + dir + " of the instance " + instance
}

// dataOwner gives the instance of a data folder: the instance of the container that mounts the
// folder at /data, else the instance of config.json when its data_dir is the folder. It gives
// "" when no instance is known.
func dataOwner(dir string, cfg Config, mounts []engine.Mount) string {
	for _, m := range mounts {
		if m.Target == "/data" && engine.Within(m.Source, dir) && engine.Within(dir, m.Source) {
			return m.Instance
		}
	}
	if cfg.DataDir != "" && engine.Within(cfg.DataDir, dir) && engine.Within(dir, cfg.DataDir) {
		return cfg.Instance
	}
	return ""
}

// mounts gives each bind mount of each container with the labels of the CLI and of an
// instance. The list of the containers can give a different path for the host folder, for
// example /host_mnt/private/tmp/x on Docker Desktop. Thus the inspect of each container gives
// the path. When the inspect fails, the path of the list counts. It gives nil when there is no
// engine, or when the engine cannot list the containers.
func (u *Uninstaller) mounts(ctx context.Context) []engine.Mount {
	if u.Engine == nil {
		return nil
	}
	cl, err := u.Engine.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil
	}
	var out []engine.Mount
	for _, c := range cl.Items {
		inst := c.Labels[LabelInstance]
		if c.Labels[LabelManagedBy] != ManagedByValue || inst == "" {
			continue
		}
		if r, err := u.Engine.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{}); err == nil {
			out = append(out, engine.HostMounts(r.Container)...)
			continue
		}
		name := c.ID
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		for _, m := range c.Mounts {
			if m.Type == mount.TypeBind && m.Source != "" {
				out = append(out, engine.Mount{Instance: inst, Container: name, Source: m.Source, Target: m.Destination})
			}
		}
	}
	return out
}

// accountInstances gives the instance of each account.
func accountInstances(accounts []string) []string {
	var out []string
	for _, a := range accounts {
		out = appendNew(out, keys.InstanceOf(a))
	}
	return out
}

// instancesExcept gives each instance of list that is not instance.
func instancesExcept(list []string, instance string) []string {
	var out []string
	for _, i := range list {
		if i != instance {
			out = appendNew(out, i)
		}
	}
	return out
}

// union gives each value of a and b one time, sorted.
func union(a, b []string) []string {
	var out []string
	for _, v := range append(append([]string(nil), a...), b...) {
		out = appendNew(out, v)
	}
	sort.Strings(out)
	return out
}

// holds tells if list holds v.
func holds(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func imageRef(it manifest.Item) string {
	if it.ID != "" {
		return it.ID
	}
	return it.Name
}

// Show writes the list of items and the size of the mail data.
func (u *Uninstaller) Show(ctx context.Context, p *Plan) {
	w := u.out()
	fmt.Fprintf(w, "mai uninstall removes these items of the instance %q:\n", p.Instance)
	for _, c := range p.Containers {
		fmt.Fprintf(w, "  container  %s (with its anonymous volumes)\n", c)
	}
	for _, v := range p.Volumes {
		fmt.Fprintf(w, "  volume     %s\n", v)
	}
	for _, it := range p.Images {
		fmt.Fprintf(w, "  image      %s %s\n", it.Name, it.ID)
	}
	for _, it := range p.AppEntries {
		fmt.Fprintf(w, "  app entry  %s in %s\n", it.Name, it.Path)
	}
	for _, it := range p.AppFiles {
		fmt.Fprintf(w, "  app file   %s (only when it holds nothing else)\n", it.Path)
	}
	for _, k := range p.Keys {
		fmt.Fprintf(w, "  key        %s (in %s)\n", k, u.Keys.Path())
	}
	note := "an item of mai 3.0 or 3.1"
	switch {
	case errors.Is(p.LegacyErr, keychain.ErrUnavailable):
		note = "the manifest records it, and the keychain is not available"
	case p.LegacyErr != nil:
		note = "if the item exists"
	}
	for _, k := range p.Legacy {
		fmt.Fprintf(w, "  keychain   %s / %s (%s)\n", keychain.Service, k, note)
	}
	if p.LegacyErr != nil && !errors.Is(p.LegacyErr, keychain.ErrUnavailable) {
		fmt.Fprintf(w, "  keychain   %s: not examined: %v\n", keychain.Service, p.LegacyErr)
	}
	for _, f := range p.Paths {
		fmt.Fprintf(w, "  folder     %s\n", f)
	}
	for _, f := range p.Unowned {
		fmt.Fprintf(w, "The data folder %s stays. %s.\n", f, unownedText)
	}
	for _, k := range p.mounted {
		fmt.Fprintf(w, "The folder %s stays, because %s.\n", k.path, k.detail)
	}
	if u.Binary != nil && len(p.Others) == 0 {
		fmt.Fprintf(w, "  binary     %s\n", u.Binary.Path)
	}
	if len(p.Others) > 0 {
		what := "the files of the CLI that they use stay"
		if len(p.KeptEntries) > 0 {
			what += ", the app entries of mai connect stay"
		}
		if u.Binary != nil {
			what += ", and the binary " + u.Binary.Path + " stays"
		}
		fmt.Fprintf(w, "The instances %s have keys or records. Thus %s.\n", strings.Join(p.Others, ", "), what)
	}
	fmt.Fprintf(w, "Size of the mail data: %s\n", u.dataSize(ctx, p))
	fmt.Fprintln(w, "CAUTION: this deletes all mail and all copies. Use --backup PATH to keep a copy.")
}

func (u *Uninstaller) dataSize(ctx context.Context, p *Plan) string {
	var total int64
	known := false
	if du, err := u.Engine.DiskUsage(ctx, client.DiskUsageOptions{Volumes: true, Verbose: true}); err == nil {
		for _, v := range du.Volumes.Items {
			for _, name := range p.Volumes {
				if v.Name == name && v.UsageData != nil && v.UsageData.Size >= 0 {
					total += v.UsageData.Size
					known = true
				}
			}
		}
	}
	// The data folder of config.json can be of a different instance. Thus only the data
	// folders of the instance count.
	for _, dir := range p.DataDirs {
		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if info, ierr := d.Info(); ierr == nil {
					total += info.Size()
					known = true
				}
			}
			return nil
		})
	}
	if !known {
		return "unknown"
	}
	return HumanSize(total)
}

// HumanSize gives a size in bytes as text with a binary unit.
func HumanSize(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// Confirm wants the typed word "uninstall", unless yes is true.
func Confirm(in io.Reader, out io.Writer, yes bool) bool {
	if yes {
		return true
	}
	if in == nil {
		return false
	}
	fmt.Fprint(out, "Type uninstall to agree: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return strings.TrimSpace(line) == "uninstall"
}

// Run does the complete uninstall and returns the exit code.
func (u *Uninstaller) Run(ctx context.Context) int {
	w := u.out()
	p, err := u.MakePlan()
	if err != nil {
		fmt.Fprintln(w, "mai uninstall:", err)
		return 1
	}
	u.Show(ctx, p)
	if !Confirm(u.In, w, u.Yes) {
		fmt.Fprintln(w, "mai uninstall: stopped. No item was removed.")
		return 1
	}
	if u.Backup != "" {
		r, err := u.Engine.ContainerInspect(ctx, p.Instance, client.ContainerInspectOptions{})
		if err == nil && (r.Container.Config == nil || r.Container.Config.Labels[LabelManagedBy] != ManagedByValue) {
			err = errors.New("the container " + p.Instance + " has no label of the CLI")
		}
		if err == nil {
			err = WriteBackup(ctx, u.Engine, r.Container.ID, u.Backup)
		}
		if err != nil {
			fmt.Fprintln(w, "mai uninstall: the backup failed, thus no item was removed:", err)
			return 1
		}
		fmt.Fprintln(w, "Backup written to", u.Backup)
	}
	var ks KeySource
	if u.Keys != nil {
		ks = u.Keys
	}
	fmt.Fprintln(w, u.endLeases(ctx, p, ks))
	rs := u.Remove(ctx, p)
	rs = append(rs, u.Search(ctx, p.Instance)...)
	switch {
	case u.Binary != nil && legacyStays(rs):
		// A subsequent uninstall removes the keychain items that stay, thus the CLI stays.
		rs = append(rs, Result{Item: "binary " + u.Binary.Path, State: Kept,
			Detail: "it stays, because keychain items stay. Do the step of each keychain item, and then run " + p.again() + " again"})
	case u.Binary != nil && len(p.Others) > 0:
		// The other instances need the CLI, for example for the bridges of their apps.
		rs = append(rs, Result{Item: "binary " + u.Binary.Path, State: Kept,
			Detail: "it stays, because the instances " + strings.Join(p.Others, ", ") + " have keys or records. " +
				"To remove the binary, run mai uninstall --instance NAME for each of these instances"})
	case u.Binary != nil:
		rs = append(rs, u.Binary.Remove())
	}
	Report(w, rs)
	if Failed(rs) {
		return 1
	}
	return 0
}

// endLeases ends the leases of the instance on the host port of its container. The port of
// config.json can be the port of a different instance, thus the owner key of the instance goes
// only to the port of its container. When that port is not known, no key goes, and the remove
// of the container ends the leases.
func (u *Uninstaller) endLeases(ctx context.Context, p *Plan, ks KeySource) string {
	base := u.BaseURL
	if base == "" {
		port, why := u.serverPort(ctx, p.Instance)
		if port == 0 {
			return "Leases: not ended, because " + why + ". The remove of the container ends them."
		}
		base = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	return EndLeases(ctx, u.HTTP, base, ks, p.Instance)
}

// serverPort gives the host port of the HTTP API of the container of the instance, when the
// container runs. Else it gives 0 and the cause.
func (u *Uninstaller) serverPort(ctx context.Context, instance string) (int, string) {
	r, err := u.Engine.ContainerInspect(ctx, instance, client.ContainerInspectOptions{})
	switch {
	case IsNotFound(err):
		return 0, "the instance " + instance + " has no container"
	case err != nil:
		return 0, "the examination of the container " + instance + " failed: " + err.Error()
	}
	c := r.Container
	switch {
	case c.Config == nil || !managedBy(c.Config.Labels, instance):
		return 0, "the container " + instance + " has no label of the instance " + instance
	case c.State == nil || !c.State.Running:
		return 0, "the container " + instance + " does not run"
	}
	if port := engine.HostPort(c); port != 0 {
		return port, ""
	}
	return 0, "the container " + instance + " has no host port of the HTTP API"
}

// Report writes one line for each result.
func Report(w io.Writer, rs []Result) {
	for _, r := range rs {
		if r.Detail != "" {
			fmt.Fprintf(w, "%-8s %s: %s\n", r.State, r.Item, r.Detail)
		} else {
			fmt.Fprintf(w, "%-8s %s\n", r.State, r.Item)
		}
	}
}

func check(item string, removeErr error, exists func() (bool, error)) Result {
	ok, err := exists()
	switch {
	case err != nil:
		return Result{Item: item, State: Failure, Detail: "check failed: " + err.Error()}
	case ok && removeErr != nil:
		return Result{Item: item, State: Failure, Detail: removeErr.Error()}
	case ok:
		return Result{Item: item, State: Failure, Detail: "it still exists"}
	}
	return Result{Item: item, State: Gone}
}

func engineExists(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func ignoreNotFound(err error) error {
	if IsNotFound(err) {
		return nil
	}
	return err
}

// Remove removes each item of the plan, and does a check of each one after its remove.
func (u *Uninstaller) Remove(ctx context.Context, p *Plan) []Result {
	var rs []Result
	removed := map[string]bool{}
	for _, c := range p.Containers {
		r, err := u.Engine.ContainerInspect(ctx, c, client.ContainerInspectOptions{})
		if IsNotFound(err) {
			rs = append(rs, Result{Item: "container " + c, State: Gone})
			continue
		}
		if err != nil {
			rs = append(rs, Result{Item: "container " + c, State: Failure, Detail: err.Error()})
			continue
		}
		// A container with no label of the CLI is not of this instance, for example the
		// container of a 2.x install. The uninstall never removes it.
		var labels map[string]string
		if r.Container.Config != nil {
			labels = r.Container.Config.Labels
		}
		if labels[LabelManagedBy] != ManagedByValue {
			rs = append(rs, Result{Item: "container " + c, State: Kept, Detail: "it has no label of the CLI; for a 2.x install, use mai uninstall --legacy"})
			continue
		}
		// A container goes only when its label tells that it is of this instance.
		if labels[LabelInstance] != p.Instance {
			rs = append(rs, Result{Item: "container " + c, State: Kept, Detail: "it has no label of the instance " + p.Instance})
			continue
		}
		id := r.Container.ID
		_, err = u.Engine.ContainerRemove(ctx, id, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true})
		rs = append(rs, check("container "+c, ignoreNotFound(err), func() (bool, error) {
			_, e := u.Engine.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
			return engineExists(e)
		}))
		removed[id] = true
	}
	for _, v := range p.Volumes {
		// A volume goes only when its labels tell that it is of this instance.
		r, err := u.Engine.VolumeInspect(ctx, v, client.VolumeInspectOptions{})
		if IsNotFound(err) {
			rs = append(rs, Result{Item: "volume " + v, State: Gone})
			continue
		}
		if err != nil {
			rs = append(rs, Result{Item: "volume " + v, State: Failure, Detail: err.Error()})
			continue
		}
		if !managedBy(r.Volume.Labels, p.Instance) {
			rs = append(rs, Result{Item: "volume " + v, State: Kept, Detail: "it has no label of the instance " + p.Instance})
			continue
		}
		_, err = u.Engine.VolumeRemove(ctx, v, client.VolumeRemoveOptions{Force: true})
		rs = append(rs, check("volume "+v, ignoreNotFound(err), func() (bool, error) {
			_, e := u.Engine.VolumeInspect(ctx, v, client.VolumeInspectOptions{})
			return engineExists(e)
		}))
	}
	for _, it := range p.Images {
		r, owner := u.removeImage(ctx, it, removed)
		switch {
		case r.State != Kept || owner == "" || owner == p.Instance:
			// The record goes with the records of the instance.
		case u.knows(p, owner):
			it.Instance = owner
			p.keptImages = append(p.keptImages, it)
			r.Detail += ". Its record goes to the instance " + owner + ", thus the uninstall of " + owner + " removes it"
		default:
			// The container can be of a different state folder, for example of a CLI with a
			// different MAI_HOME. A record of its instance keeps the manifest after the
			// uninstall of the last instance, and its uninstall removes that container.
			r.Detail += ". This state folder has no keys and no records of the instance " + owner +
				" of that container. Thus the image stays with no record. Remove it yourself when no container uses it"
		}
		rs = append(rs, r)
	}
	for _, it := range p.AppEntries {
		item := "app entry " + it.Name + " in " + it.Path
		if RemoveAppEntry == nil {
			fmt.Fprintf(u.out(), "WARNING: this CLI cannot remove the entry %s in %s. Remove it yourself.\n", it.Name, it.Path)
			rs = append(rs, Result{Item: item, State: Skipped, Detail: "remove it yourself"})
			continue
		}
		if err := RemoveAppEntry(it); err != nil {
			if IsKept != nil && IsKept(err) {
				rs = append(rs, Result{Item: item, State: Skipped, Detail: "it stays, because the file holds content that needs it"})
				continue
			}
			rs = append(rs, Result{Item: item, State: Failure, Detail: err.Error()})
			continue
		}
		rs = append(rs, Result{Item: item, State: Gone})
	}
	for _, it := range p.KeptEntries {
		rs = append(rs, Result{Item: "app entry " + it.Name + " in " + it.Path, State: Kept,
			Detail: "it stays, because the instances " + strings.Join(p.Others, ", ") + " have keys or records. " +
				p.entryText() + " To remove it, run mai disconnect " + it.Name})
	}
	for _, it := range p.KeptFiles {
		rs = append(rs, Result{Item: "app file " + it.Path, State: Kept, Detail: "it holds an app entry that stays"})
	}
	for _, it := range p.AppFiles {
		item := "app file " + it.Path
		if RemoveCreatedAppFile == nil {
			rs = append(rs, Result{Item: item, State: Skipped, Detail: "the file stays"})
			continue
		}
		gone, err := RemoveCreatedAppFile(it)
		switch {
		case err != nil:
			rs = append(rs, Result{Item: item, State: Failure, Detail: err.Error()})
		case !gone:
			rs = append(rs, Result{Item: item, State: Skipped, Detail: "it holds content that is not of mai, thus it stays"})
		default:
			rs = append(rs, Result{Item: item, State: Gone})
		}
	}
	var keep []keptPath
	var keyOthers []string
	if u.Keys != nil {
		rs, keyOthers = u.removeKeys(rs, p)
		if len(keyOthers) > 0 {
			fmt.Fprintf(u.out(), "The key file %s stays, because it holds the keys of the instances: %s\n",
				u.Keys.Path(), strings.Join(keyOthers, ", "))
		}
	}
	rs, keep = u.keepManifest(rs, keep, p, keyOthers)
	// The remove of a folder never removes the key file and its lock file. A bridge or a
	// command of a different instance can write a key to the key file during the uninstall,
	// for example when it moves its key from the keychain. While a different instance has keys or
	// records, the key file stays. Else dropKeys removes it with the lock, and only when it
	// holds no key.
	if u.Keys != nil {
		text := ""
		switch {
		case len(keyOthers) > 0:
			text = "the key file with the keys of a different instance"
		case len(p.Others) > 0 && exists(u.Keys.Path()):
			text = "the key file, because the instances " + strings.Join(p.Others, ", ") + " can write their keys to it"
		}
		// The key file is the first path of keep.
		keep = append([]keptPath{{u.Keys.Path(), text}, {u.Keys.Path() + ".lock", ""}}, keep...)
	}
	keysDone, keysGone := false, false
	// A folder that a container of a different instance mounts stays, for example its data
	// folder in a folder of the CLI.
	var mounted []engine.Mount
	for _, m := range u.mounts(ctx) {
		if m.Instance != p.Instance {
			mounted = append(mounted, m)
			keep = append(keep, keptPath{m.Source, "the folder " + m.Source + " that the container " + m.Container +
				" of the instance " + m.Instance + " mounts"})
		}
	}
	keep = append(keep, p.keptData...)
	paths := make([]string, 0, len(keep))
	for _, k := range keep {
		paths = append(paths, k.path)
	}
	for _, f := range p.Unowned {
		rs = append(rs, Result{Item: "folder " + f, State: Kept, Detail: unownedText})
	}
	for _, k := range p.mounted {
		rs = append(rs, Result{Item: "folder " + k.path, State: Kept, Detail: k.detail})
	}
	for _, f := range p.Paths {
		if f == "" {
			continue
		}
		// A container can start after the plan, thus the remove examines the mounts again.
		if m, ok := mountHolds(mounted, f); ok {
			rs = append(rs, Result{Item: "folder " + f, State: Kept, Detail: mountedText(m)})
			continue
		}
		if holdsAny(f, paths) {
			if _, err := removeExcept(f, paths); err != nil {
				rs = append(rs, Result{Item: "folder " + f, State: Failure, Detail: err.Error()})
				continue
			}
			if u.Keys != nil && len(p.Others) == 0 && within(f, u.Keys.Path()) {
				if !keysDone {
					keysDone = true
					rs, keysGone = u.dropKeys(rs, p, &keep[0])
				}
				if keysGone {
					removeEmpty(filepath.Dir(u.Keys.Path()), f)
				}
			}
			if !exists(f) {
				rs = append(rs, Result{Item: "folder " + f, State: Gone})
				continue
			}
			rs = append(rs, Result{Item: "folder " + f, State: Kept, Detail: keptText(f, keep)})
			continue
		}
		err := os.RemoveAll(f)
		rs = append(rs, check("folder "+f, err, func() (bool, error) {
			_, e := os.Lstat(f)
			if errors.Is(e, os.ErrNotExist) {
				return false, nil
			}
			return e == nil, e
		}))
	}
	return rs
}

// dropKeys removes the key file when no other instance has keys or records. The remove occurs
// with the lock of the key file, and only when the file holds no key (keys.File.RemoveEmpty).
// A key that a different process wrote after the remove of the keys stays: a key of a
// different instance with the key file, and a key of the instance as a failure. It sets the
// text of the key file in k when the file stays. It gives true when the key file is gone.
func (u *Uninstaller) dropKeys(rs []Result, p *Plan, k *keptPath) ([]Result, bool) {
	rest, err := u.Keys.RemoveEmpty()
	if err != nil {
		k.text = "the key file, because the uninstall cannot examine it"
		return append(rs, Result{Item: "key file " + u.Keys.Path(), State: Failure, Detail: err.Error()}), false
	}
	if len(rest) == 0 {
		return rs, true
	}
	var others []string
	for _, a := range rest {
		if keys.InstanceOf(a) != p.Instance {
			others = appendNew(others, keys.InstanceOf(a))
			continue
		}
		if r := lateKey(a, p.again()); !reported(rs, r) {
			rs = append(rs, r)
		}
	}
	k.text = "the key file with a key that a different process wrote during the remove"
	if len(others) > 0 {
		k.text = "the key file with the keys of a different instance"
		sort.Strings(others)
		fmt.Fprintf(u.out(), "The key file %s stays, because a different process wrote the keys of the instances %s to it during the remove.\n",
			u.Keys.Path(), strings.Join(others, ", "))
	}
	return rs, false
}

// lateKey is the failure of a key of the instance that a different process wrote to the key
// file during the remove. again is the command that does the uninstall again.
func lateKey(account, again string) Result {
	return Result{Item: "key " + account, State: Failure, Detail: "a different process wrote the key during the remove. " +
		"Stop each app that runs mai mcp, and then run " + again + " again"}
}

// reported tells if rs holds the result r.
func reported(rs []Result, r Result) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

// removeEmpty removes the folder dir, and then each folder that holds it up to the folder
// root, while the folder is empty. A folder that holds a file stays, and the folders that hold
// it also stay.
func removeEmpty(dir, root string) {
	for d := dir; within(root, d); d = filepath.Dir(d) {
		if err := os.Remove(d); err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
		if filepath.Dir(d) == d {
			return
		}
	}
}

// exists tells if a file or a folder is at the path. An error of the examination counts as a
// path that exists.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}

// unownedText tells why a data folder of a record of mai 3.0 or 3.1 stays.
const unownedText = "The manifest of mai 3.0 or 3.1 does not tell its instance, and no container and no config.json tell it. " +
	"Remove it yourself when no instance uses it"

// keptFolder is a folder that stays, with the text that tells why.
type keptFolder struct{ path, detail string }

// mountedText tells that a container of a different instance mounts a folder.
func mountedText(m engine.Mount) string {
	return "the container " + m.Container + " of the instance " + m.Instance + " mounts the folder " + m.Source
}

// mountHolds gives the mount of mounts whose folder is f or holds f.
func mountHolds(mounts []engine.Mount, f string) (engine.Mount, bool) {
	for _, m := range mounts {
		if engine.Within(m.Source, f) {
			return m, true
		}
	}
	return engine.Mount{}, false
}

// keptPath is a file or a folder that stays in a folder of the CLI, with its text for the
// report. A path with no text stays with the path before it, for example a lock file.
type keptPath struct {
	path, text string
}

// keptText tells which paths of keep stay in the folder root.
func keptText(root string, keep []keptPath) string {
	var texts []string
	for _, k := range keep {
		if k.text != "" && within(root, k.path) {
			texts = append(texts, k.text)
		}
	}
	switch len(texts) {
	case 0:
		return "only the files of a different instance stay"
	case 1:
		return "only " + texts[0] + " stays"
	}
	return "only " + strings.Join(texts[:len(texts)-1], ", ") + " and " + texts[len(texts)-1] + " stay"
}

// entriesFolder is the folder in the state folder where mai connect keeps a record of each
// entry that it wrote in a file of an app. mai disconnect and mai uninstall read these records.
const entriesFolder = "entries"

// keepManifest removes the records of the instance from the manifest, and gives the paths that
// stay for the other instances. The record of each legacy keychain item of the instance that
// stays also stays. The manifest stays while it holds a record. The configuration file stays
// when it is of a different instance that has keys or records. It also sets p.Others.
func (u *Uninstaller) keepManifest(rs []Result, keep []keptPath, p *Plan, keyOthers []string) ([]Result, []keptPath) {
	stay := legacyStay(rs)
	recOthers := instancesExcept(p.Manifest.Instances(), p.Instance)
	p.Others = union(keyOthers, recOthers)
	// The record of each app entry and file that stays gets the instance EntryOwner. The
	// record of each image that stays gets the instance of the container that uses it.
	// A record of mai 3.0 or 3.1 with no instance stays as it is.
	var moved []manifest.Item
	for _, it := range append(append([]manifest.Item(nil), p.KeptEntries...), p.KeptFiles...) {
		if it.Instance == "" {
			continue
		}
		it.Instance = p.EntryOwner
		moved = append(moved, it)
	}
	entries := len(p.KeptEntries)+len(p.KeptFiles) > 0
	moved = append(moved, p.keptImages...)
	if len(stay) > 0 || len(recOthers) > 0 || entries || len(moved) > 0 {
		text := "the manifest with the records of the keychain items"
		switch {
		case len(recOthers) > 0 || len(p.keptImages) > 0:
			text = "the manifest with the records of a different instance"
		case entries:
			text = "the manifest with the records of the app entries that stay"
		}
		// The manifest stays also when the write fails, because it can hold the records of a
		// different instance.
		keep = append(keep, keptPath{p.Manifest.Path(), text})
		if err := keepRecords(p.Manifest, p.Instance, stay, moved, append(append([]manifest.Item(nil), p.AppEntries...), p.AppFiles...)); err != nil {
			rs = append(rs, Result{Item: "manifest " + p.Manifest.Path(), State: Failure, Detail: err.Error()})
		} else {
			if len(stay) > 0 {
				fmt.Fprintf(u.out(), "The manifest %s stays with the record of the keychain items %s. "+
					"Thus a subsequent %s can remove them.\n", p.Manifest.Path(), strings.Join(stay, ", "), p.again())
			}
			if len(recOthers) > 0 {
				fmt.Fprintf(u.out(), "The manifest %s stays, because it holds the records of the instances: %s\n",
					p.Manifest.Path(), strings.Join(recOthers, ", "))
			}
		}
		if (len(recOthers) > 0 || entries) && len(p.Manifest.Of(manifest.AppEntry)) > 0 {
			keep = append(keep, keptPath{filepath.Join(u.Dirs.State, entriesFolder), "the records of the app entries of a different instance"})
		}
	}
	if holds(p.Others, p.Config.Instance) {
		cfg := filepath.Join(u.Dirs.Config, ConfigFile)
		if _, err := os.Lstat(cfg); err == nil {
			keep = append(keep, keptPath{cfg, "the configuration file of the instance " + p.Config.Instance})
			fmt.Fprintf(u.out(), "The configuration file %s stays, because it is of the instance %s.\n", cfg, p.Config.Instance)
		}
	}
	return rs, keep
}

// removeKeys removes the keys of the instance from the key file and the legacy keychain
// items of the instance, and does a check of each one. It gives the other instances whose keys
// stay in the key file. It gives a failure for a key of the instance that stays in that file.
func (u *Uninstaller) removeKeys(rs []Result, p *Plan) ([]Result, []string) {
	for _, k := range p.Keys {
		err := u.Keys.Delete(k)
		if errors.Is(err, keys.ErrNotFound) {
			err = nil
		}
		rs = append(rs, check("key "+k, err, func() (bool, error) { return u.Keys.Has(k) }))
	}
	// A key that a different process wrote after the plan also goes.
	extra, err := u.Keys.DeleteInstance(p.Instance)
	if err != nil {
		rs = append(rs, Result{Item: "keys of " + p.Instance + " in " + u.Keys.Path(), State: Failure, Detail: err.Error()})
	}
	for _, k := range extra {
		rs = append(rs, Result{Item: "key " + k, State: Gone})
	}
	again := p.again()
	for _, k := range p.Legacy {
		item := legacyPrefix + k
		if errors.Is(p.LegacyErr, keychain.ErrUnavailable) {
			rs = append(rs, Result{Item: item, State: Failure, Detail: "the keychain is not available (" + p.LegacyErr.Error() +
				"). Make the keychain available, for example in a desktop session of the user, and then run " + again + " again"})
			continue
		}
		err := u.Keys.DeleteLegacy(k)
		rs = append(rs, check(item, err, func() (bool, error) { return u.Keys.HasLegacyItem(k) }))
	}
	// A bridge that read a keychain item before its delete can write the key to the key file
	// after the first remove. Thus the keys of the instance go again.
	late, err := u.Keys.DeleteInstance(p.Instance)
	if err != nil {
		rs = append(rs, Result{Item: "keys of " + p.Instance + " in " + u.Keys.Path(), State: Failure, Detail: err.Error()})
	}
	for _, k := range late {
		rs = append(rs, Result{Item: "key " + k, State: Gone})
	}
	rest, err := u.Keys.Accounts()
	if err != nil {
		rs = append(rs, Result{Item: "key file " + u.Keys.Path(), State: Failure, Detail: err.Error()})
		// The key file stays, because the uninstall cannot tell if it holds the keys of a
		// different instance.
		return rs, []string{"unknown"}
	}
	var others, mine []string
	for _, a := range rest {
		if keys.InstanceOf(a) == p.Instance {
			mine = append(mine, a)
			continue
		}
		others = appendNew(others, keys.InstanceOf(a))
	}
	// The remove of a folder never removes the key file. Thus a key of the instance that a
	// different process wrote after the remove of the keys stays in it.
	for _, a := range mine {
		rs = append(rs, lateKey(a, again))
	}
	sort.Strings(others)
	return rs, others
}

// entryText tells which instance the bridge of an app entry that stays uses after the
// uninstall. An entry names no instance, thus its bridge uses the instance of config.json.
// config.json stays only when its instance stays. Else the bridge uses the default instance,
// and the text gives the step that makes EntryOwner the instance of config.json.
func (p *Plan) entryText() string {
	if holds(p.Others, p.Config.Instance) {
		return "The entry names no instance, thus its bridge uses the instance of config.json, which is " + p.Config.Instance + "."
	}
	return "The entry names no instance, thus its bridge uses the instance of config.json. config.json goes, thus its bridge then uses " +
		"the default instance " + DefaultInstance + " on the default port. Run mai start --instance " + p.EntryOwner + " to make " +
		p.EntryOwner + " the instance of config.json, with the port of its container."
}

// again gives the command that does the uninstall of the instance again. A command with no
// --instance acts on the instance of config.json, and config.json can stay for a different
// instance. Thus the command names --instance, unless the instance is the default instance
// and config.json names it. That config.json goes with the instance.
func (p *Plan) again() string {
	if p.Instance == DefaultInstance && p.Config.Instance == p.Instance {
		return "mai uninstall"
	}
	return "mai uninstall --instance " + p.Instance
}

// legacyPrefix is the start of the name of the result of a legacy keychain item.
const legacyPrefix = "keychain " + keychain.Service + " / "

// legacyStay gives the account of each legacy keychain item that the remove did not remove.
func legacyStay(rs []Result) []string {
	var out []string
	for _, r := range rs {
		if r.State == Failure && strings.HasPrefix(r.Item, legacyPrefix) {
			out = appendNew(out, strings.TrimPrefix(r.Item, legacyPrefix))
		}
	}
	return out
}

// legacyStays tells if a legacy keychain item stays after the remove.
func legacyStays(rs []Result) bool { return len(legacyStay(rs)) > 0 }

// keepRecords removes each record of the instance from the manifest, except the record of
// each legacy keychain item of stay. A subsequent uninstall reads the records, and removes the
// items. The records of the other instances stay. The record of each item of moved also
// stays, with the instance of the item. A record with no instance goes only when it is of an
// item of gone, which the uninstall removed.
func keepRecords(m *manifest.Manifest, instance string, stay []string, moved, gone []manifest.Item) error {
	want := map[string]bool{}
	for _, a := range stay {
		want[a] = true
	}
	kept := func(it manifest.Item) bool {
		for _, e := range moved {
			if e.Type == it.Type && e.Name == it.Name && e.Path == it.Path && e.Pointer == it.Pointer {
				return true
			}
		}
		return false
	}
	removed := func(it manifest.Item) bool {
		for _, g := range gone {
			if g.Type == it.Type && g.Name == it.Name && g.Path == it.Path && g.Pointer == it.Pointer {
				return true
			}
		}
		return false
	}
	for _, it := range append([]manifest.Item(nil), m.Items...) {
		mine := it.Instance == instance || it.Instance == "" && removed(it)
		if !mine || (it.Type == manifest.KeychainKey && want[it.Name]) || kept(it) {
			continue
		}
		if err := m.Remove(it); err != nil {
			return err
		}
	}
	for _, a := range stay {
		if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: a}); err != nil {
			return err
		}
	}
	for _, it := range moved {
		if err := m.Add(it); err != nil {
			return err
		}
	}
	return nil
}

// holdsAny tells if a path of keep is root or is in the folder root.
func holdsAny(root string, keep []string) bool {
	for _, k := range keep {
		if within(root, k) {
			return true
		}
	}
	return false
}

// within tells if path is root or is in the folder root. It resolves the symbolic links,
// because an engine can give a mount with a different path of the same folder, for example
// /private/var for /var on macOS.
func within(root, path string) bool { return engine.Within(root, path) }

// removeExcept removes root and all that it holds, except each path of keep and the folders
// that hold such a path. It gives true when a path of keep is in root.
func removeExcept(root string, keep []string) (bool, error) {
	holds := false
	for _, k := range keep {
		if within(root, k) && within(k, root) {
			return true, nil
		}
		if within(root, k) {
			holds = true
		}
	}
	if !holds {
		return false, os.RemoveAll(root)
	}
	fi, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.IsDir() {
		// A link is not the folder that holds the key file. Remove only the link.
		return false, os.Remove(root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return true, err
	}
	var errs []error
	for _, e := range entries {
		if _, err := removeExcept(filepath.Join(root, e.Name()), keep); err != nil {
			errs = append(errs, err)
		}
	}
	return true, errors.Join(errs...)
}

// knows tells if the instance has keys in the key file or records in the manifest of this
// state folder. An error of the key file gives false.
func (u *Uninstaller) knows(p *Plan, instance string) bool {
	if holds(p.Others, instance) || holds(p.Manifest.Instances(), instance) {
		return true
	}
	if u.Keys == nil {
		return false
	}
	accounts, err := u.Keys.OfInstance(instance)
	return err == nil && len(accounts) > 0
}

// removeImage removes one image of the instance. An image that a different container uses
// stays. Then owner is the instance of that container, or "" for a container with no label of
// the CLI.
func (u *Uninstaller) removeImage(ctx context.Context, it manifest.Item, removed map[string]bool) (r Result, owner string) {
	ref := imageRef(it)
	item := "image " + ref
	info, err := u.Engine.ImageInspect(ctx, ref)
	if IsNotFound(err) {
		return Result{Item: item, State: Gone}, ""
	}
	if err != nil {
		return Result{Item: item, State: Failure, Detail: err.Error()}, ""
	}
	list, err := u.Engine.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return Result{Item: item, State: Failure, Detail: err.Error()}, ""
	}
	for _, c := range list.Items {
		if removed[c.ID] {
			continue
		}
		if c.ImageID == info.ID || c.ImageID == ref {
			if c.Labels[LabelManagedBy] == ManagedByValue {
				owner = c.Labels[LabelInstance]
			}
			return Result{Item: item, State: Kept, Detail: "a different container uses it: " + strings.Join(c.Names, ",")}, owner
		}
	}
	_, err = u.Engine.ImageRemove(ctx, ref, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	return check(item, ignoreNotFound(err), func() (bool, error) {
		_, e := u.Engine.ImageInspect(ctx, ref)
		return engineExists(e)
	}), ""
}

func managedFilter(instance string) client.Filters {
	return client.Filters{}.
		Add("label", LabelManagedBy+"="+ManagedByValue).
		Add("label", LabelInstance+"="+instance)
}

func managedBy(labels map[string]string, instance string) bool {
	return labels[LabelManagedBy] == ManagedByValue && labels[LabelInstance] == instance
}

// Search finds objects of the engine with the labels of the instance. Each object that it
// finds is a failure.
func (u *Uninstaller) Search(ctx context.Context, instance string) []Result {
	var rs []Result
	cl, err := u.Engine.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: managedFilter(instance)})
	if err != nil {
		rs = append(rs, Result{Item: "search of containers", State: Failure, Detail: err.Error()})
	}
	for _, c := range cl.Items {
		if managedBy(c.Labels, instance) {
			rs = append(rs, Result{Item: "container " + strings.Join(c.Names, ","), State: Failure, Detail: "it has the labels of mai and still exists"})
		}
	}
	vl, err := u.Engine.VolumeList(ctx, client.VolumeListOptions{Filters: managedFilter(instance)})
	if err != nil {
		rs = append(rs, Result{Item: "search of volumes", State: Failure, Detail: err.Error()})
	}
	for _, v := range vl.Items {
		if managedBy(v.Labels, instance) {
			rs = append(rs, Result{Item: "volume " + v.Name, State: Failure, Detail: "it has the labels of mai and still exists"})
		}
	}
	if len(rs) == 0 {
		rs = append(rs, Result{Item: "engine objects with the labels of " + instance, State: Gone})
	}
	return rs
}
