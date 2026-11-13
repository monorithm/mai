package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/monorithm/mai/cli/v3/internal/hook"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
)

// lease is one lease of the bridge: the pool lease, or the lease of a named session that a
// chat logged in to. The token is only in this memory.
type lease struct {
	handle string             // The handle of the chat, or "" for the pool lease.
	ctx    context.Context    // The context of the heartbeat loop.
	cancel context.CancelFunc // Stops the heartbeat loop of the lease.
	done   chan struct{}      // Closes at the end of the heartbeat loop.

	mu        sync.Mutex
	token     string
	login     string
	interval  time.Duration
	newMail   int
	updatedAt string
	ended     bool

	renewMu sync.Mutex
}

// leaseAnswer is the answer of POST /api/leases.
type leaseAnswer struct {
	Lease            string `json:"lease"`
	Session          string `json:"session"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
}

// errEnded means that the bridge ended the lease. The bridge does not renew it.
var errEnded = errors.New("the lease ended")

func (l *lease) set(a leaseAnswer) { l.setIfLive(a) }

// setIfLive keeps the lease of the answer a, and returns false when the lease ended.
func (l *lease) setIfLive(a leaseAnswer) bool {
	d := time.Duration(a.HeartbeatSeconds) * time.Second
	if d <= 0 {
		d = 30 * time.Second
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return false
	}
	l.token, l.interval = a.Lease, d
	if a.Session != "" {
		l.login = a.Session
	}
	return true
}

func (l *lease) current() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.token
}

func (l *lease) session() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.login
}

func (l *lease) wait() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.interval
}

// notice gives the notice entry of the lease.
func (l *lease) notice() hook.Notice {
	l.mu.Lock()
	defer l.mu.Unlock()
	return hook.Notice{Session: l.login, NewMail: l.newMail, UpdatedAt: l.updatedAt}
}

// finish marks the lease as ended and returns its token. After finish, the bridge does not
// renew the lease.
func (l *lease) finish() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.token
	l.ended, l.token = true, ""
	return t
}

func (l *lease) isEnded() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ended
}

// errNoKey means that the keychain has no key for the agent.
var errNoKey = errors.New("no agent key")

// request sends a lease request for the named session name, or for a pool session when
// name is "". The presence check occurs only before the first lease request of the
// process. A later request gets no prompt, because the user approved this bridge.
func (b *Bridge) request(ctx context.Context, name string) (leaseAnswer, error) {
	b.approveMu.Lock()
	defer b.approveMu.Unlock()
	if !b.approved {
		if b.Presence == nil {
			return leaseAnswer{}, errors.New("no presence check")
		}
		if err := b.Presence(b.Reason()); err != nil {
			return leaseAnswer{}, fmt.Errorf("the presence check failed: %w", err)
		}
		b.approved = true
	}
	key, err := b.Store.Get(keychain.Service, keychain.AgentAccount(b.Instance, b.Agent))
	if errors.Is(err, keychain.ErrNotFound) {
		return leaseAnswer{}, errNoKey
	}
	if err != nil {
		return leaseAnswer{}, fmt.Errorf("the keychain read failed: %w", err)
	}
	in := map[string]any{"app": b.App, "folder": b.Folder, "pid": b.PID}
	if name != "" {
		in["session"] = name
	}
	body, _ := json.Marshal(in)
	var out leaseAnswer
	if err := b.callJSON(ctx, http.MethodPost, "/api/leases", key, body, &out); err != nil {
		return leaseAnswer{}, err
	}
	if !strings.HasPrefix(out.Lease, "lease_") {
		return leaseAnswer{}, errors.New("the server gave no lease")
	}
	return out, nil
}

// renew gets a new lease for l when old is still the token of l: the same named session,
// or a pool session for the pool lease. When a different call already renewed l, renew
// does nothing. There is no new presence check.
func (b *Bridge) renew(ctx context.Context, l *lease, old string) error {
	l.renewMu.Lock()
	defer l.renewMu.Unlock()
	if l.isEnded() {
		return errEnded
	}
	if l.current() != old {
		return nil
	}
	name := ""
	if l.handle != "" {
		name = l.session()
	}
	a, err := b.request(ctx, name)
	if err != nil {
		return err
	}
	before := l.session()
	if !l.setIfLive(a) {
		// The chat logged out during the request. End the new lease at once.
		b.deleteLease(a.Lease)
		return errEnded
	}
	if l.handle != "" && a.Session != "" && a.Session != before {
		b.mu.Lock()
		if b.byLogin[before] == l.handle {
			delete(b.byLogin, before)
		}
		b.byLogin[a.Session] = l.handle
		b.mu.Unlock()
	}
	b.logf("the bridge has a new lease for the session %s", l.session())
	return nil
}

// newLease makes the lease of the answer a, with the handle of a chat or "" for the pool.
// Then call startBeats.
func (b *Bridge) newLease(handle string, a leaseAnswer) *lease {
	ctx, cancel := context.WithCancel(b.beatCtx)
	l := &lease{handle: handle, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	l.set(a)
	return l
}

// startBeats starts the heartbeat loop of l.
func (b *Bridge) startBeats(l *lease) {
	b.beats.Add(1)
	go func() {
		defer b.beats.Done()
		defer close(l.done)
		b.heartbeatLoop(l.ctx, l)
	}()
}

func (b *Bridge) heartbeatLoop(ctx context.Context, l *lease) {
	for {
		if err := b.beat(ctx, l); err != nil {
			if ctx.Err() != nil || errors.Is(err, errEnded) {
				return
			}
			if l.handle == "" {
				select {
				case b.fatal <- err:
				default:
				}
				return
			}
			b.drop(l, err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(l.wait()):
		}
	}
}

// beat sends one heartbeat for l and writes the notice file. It returns an error only when
// the server does not know the lease and no new lease is available.
func (b *Bridge) beat(ctx context.Context, l *lease) error {
	token := l.current()
	var out struct {
		Session string `json:"session"`
		NewMail int    `json:"new_mail"`
	}
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := b.callJSON(hctx, http.MethodPost, "/api/leases/current/heartbeat", token, []byte("{}"), &out)
	if ctx.Err() != nil {
		return nil
	}
	if isLeaseUnknown(err) {
		b.logf("the server does not know the lease of the session %s; the bridge asks for a new lease", l.session())
		rerr := b.renew(ctx, l, token)
		var ae *apiError
		if rerr != nil && l.handle != "" && !errors.As(rerr, &ae) && !errors.Is(rerr, errEnded) {
			// A network error does not end a named session. The next heartbeat tries again.
			b.logf("the new lease failed: %v", rerr)
			return nil
		}
		return rerr
	}
	if err != nil {
		// A network error does not stop the bridge. The next heartbeat tries again.
		b.logf("the heartbeat failed: %v", err)
		return nil
	}
	l.mu.Lock()
	if l.ended || l.token != token {
		l.mu.Unlock()
		return nil
	}
	if out.Session != "" {
		l.login = out.Session
	}
	l.newMail, l.updatedAt = out.NewMail, time.Now().UTC().Format(time.RFC3339)
	l.mu.Unlock()
	b.writeNotice()
	return nil
}

// drop removes a named lease that ended on the server and that the bridge cannot renew.
// A later tool call with its handle gets SESSION_UNKNOWN.
func (b *Bridge) drop(l *lease, why error) {
	if b.namedLease(l.handle) == l {
		b.forget(l.handle)
	}
	l.finish()
	l.cancel()
	b.logf("the session %s ended: %v", l.session(), why)
	b.writeNotice()
}

// deleteLease ends one lease on the server.
func (b *Bridge) deleteLease(token string) {
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.callJSON(ctx, http.MethodDelete, "/api/leases/current", token, nil, nil); err != nil && !isLeaseUnknown(err) {
		b.logf("the end of the lease failed: %v", err)
	}
}

// leases gives the pool lease and the named leases in login order.
func (b *Bridge) leases() (*lease, []*lease) {
	b.mu.Lock()
	defer b.mu.Unlock()
	named := make([]*lease, 0, len(b.named))
	for _, l := range b.named {
		named = append(named, l)
	}
	sort.Slice(named, func(i, j int) bool { return named[i].session() < named[j].session() })
	return b.pool, named
}

func (b *Bridge) noticePath() string { return hook.NoticePath(b.StateDir, b.AppPID, b.Agent) }

// writeNotice writes the notice file of the bridge with mode 0600 in a folder with mode
// 0700. The file has the pool session and each named session, and holds no secret.
func (b *Bridge) writeNotice() {
	b.noticeMu.Lock()
	defer b.noticeMu.Unlock()
	if b.StateDir == "" || b.noticesGone {
		return
	}
	pool, named := b.leases()
	if pool == nil {
		return
	}
	n := pool.notice()
	if n.Session == "" {
		return
	}
	for _, l := range named {
		if e := l.notice(); e.Session != "" && !l.isEnded() {
			n.Named = append(n.Named, e)
		}
	}
	dir := hook.NoticeDir(b.StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.logf("the notice folder is not available: %v", err)
		return
	}
	_ = os.Chmod(dir, 0o700)
	data, _ := json.Marshal(n)
	tmp, err := os.CreateTemp(dir, ".notice-*")
	if err != nil {
		b.logf("the notice file is not available: %v", err)
		return
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	_ = os.Chmod(name, 0o600)
	if werr != nil || cerr != nil || os.Rename(name, b.noticePath()) != nil {
		_ = os.Remove(name)
		b.logf("the notice file write failed")
	}
}

// endAll removes the notice file and ends each lease of the bridge. Stop each heartbeat
// loop first.
func (b *Bridge) endAll() {
	b.noticeMu.Lock()
	b.noticesGone = true
	if b.StateDir != "" {
		_ = os.Remove(b.noticePath())
	}
	b.noticeMu.Unlock()
	b.mu.Lock()
	all := make([]*lease, 0, 1+len(b.named))
	if b.pool != nil {
		all = append(all, b.pool)
	}
	for _, l := range b.named {
		all = append(all, l)
	}
	b.named, b.byLogin = map[string]*lease{}, map[string]string{}
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, l := range all {
		token := l.finish()
		if token == "" {
			continue
		}
		if err := b.callJSON(ctx, http.MethodDelete, "/api/leases/current", token, nil, nil); err != nil && !isLeaseUnknown(err) {
			b.logf("the end of the lease of the session %s failed: %v", l.session(), err)
		}
	}
}
