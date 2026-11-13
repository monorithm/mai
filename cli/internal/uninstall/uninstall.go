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

	"github.com/moby/moby/client"

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

// Uninstaller removes one instance and the files of the CLI.
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
	BaseURL  string  // base URL of the server; empty = http://127.0.0.1:<port>
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
	Keys       []string        // accounts of the instance in the key file
	Legacy     []string        // accounts of the instance with a legacy keychain item
	LegacyErr  error           // the error of the examination of the legacy keychain
	Paths      []string
	Manifest   *manifest.Manifest
	Config     Config
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

// MakePlan reads config.json and the manifest and gives the items to remove.
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
	for _, it := range man.Of(manifest.Container) {
		p.Containers = appendNew(p.Containers, it.Name)
	}
	p.Volumes = appendNew(p.Volumes, DataVolume(inst))
	p.Volumes = appendNew(p.Volumes, SpoolVolume(inst))
	for _, it := range man.Of(manifest.Volume) {
		p.Volumes = appendNew(p.Volumes, it.Name)
	}
	p.Images = man.Of(manifest.Image)
	p.AppEntries = man.Of(manifest.AppEntry)
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
		if p.LegacyErr != nil && !errors.Is(p.LegacyErr, keychain.ErrUnavailable) {
			// The examination failed, thus each candidate can have an item. The remove
			// deletes the item of each candidate and does a check of it.
			for _, a := range candidates {
				p.Legacy = appendNew(p.Legacy, a)
			}
		}
	}
	// A file of an app can hold content of the user after mai connect made it, thus it does
	// not go with the folders of mai.
	p.AppFiles = man.Of(manifest.File)
	for _, it := range man.Of(manifest.Folder) {
		p.Paths = appendNew(p.Paths, it.Path)
	}
	p.Paths = appendNew(p.Paths, u.Dirs.Cache)
	p.Paths = appendNew(p.Paths, u.Dirs.State)
	p.Paths = appendNew(p.Paths, u.Dirs.Config)
	return p, nil
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
	if p.LegacyErr != nil && !errors.Is(p.LegacyErr, keychain.ErrUnavailable) {
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
	if u.Binary != nil {
		fmt.Fprintf(w, "  binary     %s\n", u.Binary.Path)
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
	if p.Config.DataDir != "" {
		_ = filepath.WalkDir(p.Config.DataDir, func(_ string, d fs.DirEntry, err error) error {
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
	fmt.Fprintln(w, EndLeases(ctx, u.HTTP, u.baseURL(p), ks, p.Instance))
	rs := u.Remove(ctx, p)
	rs = append(rs, u.Search(ctx, p.Instance)...)
	if u.Binary != nil {
		rs = append(rs, u.Binary.Remove())
	}
	Report(w, rs)
	if Failed(rs) {
		return 1
	}
	return 0
}

func (u *Uninstaller) baseURL(p *Plan) string {
	if u.BaseURL != "" {
		return u.BaseURL
	}
	return fmt.Sprintf("http://127.0.0.1:%d", p.Config.Port)
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
		id := r.Container.ID
		_, err = u.Engine.ContainerRemove(ctx, id, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true})
		rs = append(rs, check("container "+c, ignoreNotFound(err), func() (bool, error) {
			_, e := u.Engine.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
			return engineExists(e)
		}))
		removed[id] = true
	}
	for _, v := range p.Volumes {
		_, err := u.Engine.VolumeRemove(ctx, v, client.VolumeRemoveOptions{Force: true})
		rs = append(rs, check("volume "+v, ignoreNotFound(err), func() (bool, error) {
			_, e := u.Engine.VolumeInspect(ctx, v, client.VolumeInspectOptions{})
			return engineExists(e)
		}))
	}
	for _, it := range p.Images {
		rs = append(rs, u.removeImage(ctx, it, removed))
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
	var keep []string
	if u.Keys != nil {
		var others []string
		rs, others = u.removeKeys(rs, p)
		if len(others) > 0 {
			keep = []string{u.Keys.Path(), u.Keys.Path() + ".lock"}
			fmt.Fprintf(u.out(), "The key file %s stays, because it holds the keys of the instances: %s\n",
				u.Keys.Path(), strings.Join(others, ", "))
		}
	}
	for _, f := range p.Paths {
		if f == "" {
			continue
		}
		if len(keep) > 0 && within(f, keep[0]) {
			if _, err := removeExcept(f, keep); err != nil {
				rs = append(rs, Result{Item: "folder " + f, State: Failure, Detail: err.Error()})
				continue
			}
			rs = append(rs, Result{Item: "folder " + f, State: Kept, Detail: "only the key file with the keys of a different instance stays"})
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

// removeKeys removes the keys of the instance from the key file and the legacy keychain
// items of the instance, and does a check of each one. It gives the instances whose keys stay
// in the key file.
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
	for _, k := range p.Legacy {
		err := u.Keys.DeleteLegacy(k)
		rs = append(rs, check("keychain "+keychain.Service+" / "+k, err, func() (bool, error) { return u.Keys.HasLegacyItem(k) }))
	}
	rest, err := u.Keys.Accounts()
	if err != nil {
		rs = append(rs, Result{Item: "key file " + u.Keys.Path(), State: Failure, Detail: err.Error()})
		// The key file stays, because the uninstall cannot tell if it holds the keys of a
		// different instance.
		return rs, []string{"unknown"}
	}
	var others []string
	for _, a := range rest {
		others = appendNew(others, keys.InstanceOf(a))
	}
	sort.Strings(others)
	return rs, others
}

// within tells if path is root or is in the folder root.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// removeExcept removes root and all that it holds, except each path of keep and the folders
// that hold such a path. It gives true when a path of keep is in root.
func removeExcept(root string, keep []string) (bool, error) {
	holds := false
	for _, k := range keep {
		if filepath.Clean(k) == filepath.Clean(root) {
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

func (u *Uninstaller) removeImage(ctx context.Context, it manifest.Item, removed map[string]bool) Result {
	ref := imageRef(it)
	item := "image " + ref
	info, err := u.Engine.ImageInspect(ctx, ref)
	if IsNotFound(err) {
		return Result{Item: item, State: Gone}
	}
	if err != nil {
		return Result{Item: item, State: Failure, Detail: err.Error()}
	}
	list, err := u.Engine.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return Result{Item: item, State: Failure, Detail: err.Error()}
	}
	for _, c := range list.Items {
		if removed[c.ID] {
			continue
		}
		if c.ImageID == info.ID || c.ImageID == ref {
			return Result{Item: item, State: Kept, Detail: "a different container uses it: " + strings.Join(c.Names, ",")}
		}
	}
	_, err = u.Engine.ImageRemove(ctx, ref, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	return check(item, ignoreNotFound(err), func() (bool, error) {
		_, e := u.Engine.ImageInspect(ctx, ref)
		return engineExists(e)
	})
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
