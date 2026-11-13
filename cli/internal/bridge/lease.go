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
//
// The pool lease can have no session. The server gives such a lease when no pool session of
// the agent is free. A tools/call on it can bind a free pool session to it. The bridge then
// sends a heartbeat at once, and the answer gives that session.
type lease struct {
	handle string             // The handle of the chat, or "" for the pool lease.
	ctx    context.Context    // The context of the heartbeat loop.
	cancel context.CancelFunc // Stops the heartbeat loop of the lease.
	done   chan struct{}      // Closes at the end of the heartbeat loop.
	soon   chan struct{}      // A value here makes the heartbeat loop send a heartbeat at once.

	mu        sync.Mutex
	token     string
	login     string
	interval  time.Duration
	newMail   int
	updatedAt string
	ended     bool

	renewMu sync.Mutex
}

// leaseAnswer is the answer of POST /api/leases. Session is "" when the answer has
// "session": null, thus a pool lease with no session.
type leaseAnswer struct {
	Lease            string `json:"lease"`
	Session          string `json:"session"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
}

// beatAnswer is the answer of a heartbeat. Session is nil for a lease with no session.
// NewMail is nil when the server cannot count the new mail.
type beatAnswer struct {
	Session *string `json:"session"`
	NewMail *int    `json:"new_mail"`
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
	if l.handle == "" || a.Session != "" {
		l.setLogin(a.Session)
	}
	return true
}

// setLogin records the session of the lease. When the session changes, the count of new
// mail goes to 0. Hold l.mu.
func (l *lease) setLogin(login string) {
	if login != l.login {
		l.login, l.newMail, l.updatedAt = login, 0, ""
	}
}

// clearLogin removes the session of the pool lease, when the pool lease has no live lease.
// It returns true when the lease had a session.
func (l *lease) clearLogin() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	had := l.login != ""
	l.setLogin("")
	return had
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

// describe names the lease in a log line or a message.
func (l *lease) describe() string {
	if s := l.session(); s != "" {
		return "the session " + s
	}
	return "the pool lease"
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

// waitRenew waits until a renew of l that runs ends. A renew that ends after finish ends
// its new lease itself (setIfLive).
func (l *lease) waitRenew() {
	l.renewMu.Lock()
	defer l.renewMu.Unlock()
}

// errNoKey means that the keychain has no key for the agent.
var errNoKey = errors.New("no agent key")

// isBadKey is true when the agent key is not valid or not in the keychain. Then no lease
// request can succeed.
func isBadKey(err error) bool {
	var ae *apiError
	return errors.Is(err, errNoKey) || (errors.As(err, &ae) && ae.Code == "BAD_KEY")
}

// isRefusal is true when the server refused the lease request with an HTTP 4xx answer and an
// error code, for example SESSION_REVOKED or SESSION_BUSY. Only a refusal ends a named
// session. A 5xx answer, an answer with no code, and a network error do not.
func isRefusal(err error) bool {
	var ae *apiError
	return errors.Is(err, errNoKey) || (errors.As(err, &ae) && ae.Code != "" && ae.Status >= 400 && ae.Status < 500)
}

// endsLease is true when the error of a renew ends l. These errors end l:
//   - The bridge ended l.
//   - The server refused the named session of l.
//   - The agent key of the pool lease is not valid.
func endsLease(l *lease, err error) bool {
	if errors.Is(err, errEnded) {
		return true
	}
	if l.handle == "" {
		return isBadKey(err)
	}
	return isRefusal(err)
}

// msgNoFreeSession is the message of NO_FREE_SESSION. It is the same as the message of the
// server.
const msgNoFreeSession = "No pool session of the agent is free. Call login with a named session, or try again later."

// unavailable gives the code and the message of a failed lease request that does not end
// the lease l. The code is the code of the server, or SERVER_UNAVAILABLE when the answer
// has no code.
func unavailable(l *lease, err error) (string, string) {
	var ae *apiError
	if errors.As(err, &ae) && ae.Code != "" {
		if ae.Code == "NO_FREE_SESSION" {
			return ae.Code, msgNoFreeSession
		}
		return ae.Code, "The bridge got no lease for " + l.describe() + ": " + ae.Message + ". Try again later."
	}
	return "SERVER_UNAVAILABLE", "The bridge got no lease for " + l.describe() + ": " + err.Error() + ". Try again later."
}

// logNoPool tells that the agent has no free pool session.
func (b *Bridge) logNoPool() {
	b.logf("the agent %s has no free pool session. A chat can call login with a named session. "+
		"A call with no session gets NO_FREE_SESSION until a pool session is free.", b.Agent)
}

// leaseRequestTimeout is the longest time that the bridge waits for the answer of a lease
// request. The server logs in to the mail server before it answers, thus the answer can
// take some seconds. The time limit of a lease request on the server (20 s) is less than
// this wait, thus the bridge gets the code of each failure.
const leaseRequestTimeout = 30 * time.Second

// request sends a lease request for the named session name, or for a pool session when
// name is "". The presence check occurs only before the first lease request of the
// process. A subsequent request gets no prompt, because the user approved this bridge.
//
// When ctx ends before the request starts, request sends no request. When ctx ends after the
// request starts, the request continues until the answer or leaseRequestTimeout. The server
// makes the lease before it answers. Thus the caller always gets the token of each lease
// that the server made, and can end it: a logout or a stop of the bridge leaves no lease on
// the server.
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
	if err := ctx.Err(); err != nil {
		return leaseAnswer{}, err
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaseRequestTimeout)
	defer cancel()
	var out leaseAnswer
	if err := b.callJSON(rctx, http.MethodPost, "/api/leases", key, body, &out); err != nil {
		return leaseAnswer{}, err
	}
	if !strings.HasPrefix(out.Lease, "lease_") {
		return leaseAnswer{}, errors.New("the server gave no lease")
	}
	return out, nil
}

// renew gets a new lease for l when old is still the token of l: the same named session,
// or a new pool lease for the pool lease. When a different call renewed l before, renew
// does nothing. There is no new presence check.
//
// When the request is not successful, l keeps its token. Thus the next call or heartbeat
// gets LEASE_UNKNOWN again and tries again. The pool lease then has no session.
//
// When the request is successful, renew writes the notice file. Thus the notice has the
// session of the new lease immediately, also when a tool call (not a heartbeat) renewed l.
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
		if l.handle == "" && l.clearLogin() {
			b.writeNotice()
		}
		return err
	}
	before := l.session()
	if !l.setIfLive(a) {
		// The chat logged out during the request. End the new lease immediately.
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
	if l.handle == "" && a.Session == "" {
		b.logNoPool()
	} else {
		b.logf("the bridge has a new lease for the session %s", l.session())
	}
	b.writeNotice()
	return nil
}

// newLease makes the lease of the answer a, with the handle of a chat or "" for the pool.
// Then call startBeats.
func (b *Bridge) newLease(handle string, a leaseAnswer) *lease {
	ctx, cancel := context.WithCancel(b.beatCtx)
	l := &lease{handle: handle, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		soon: make(chan struct{}, 1)}
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
		case <-l.soon:
		case <-time.After(l.wait()):
		}
	}
}

// beatSoon makes the heartbeat loop of l send a heartbeat at once. It does not wait.
func (l *lease) beatSoon() {
	select {
	case l.soon <- struct{}{}:
	default:
	}
}

// heartbeat sends one heartbeat with the token.
func (b *Bridge) heartbeat(ctx context.Context, token string) (beatAnswer, error) {
	var out beatAnswer
	// The time limit of a heartbeat on the server (10 s) is less than this wait.
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := b.callJSON(hctx, http.MethodPost, "/api/leases/current/heartbeat", token, []byte("{}"), &out)
	return out, err
}

// beat sends one heartbeat for l and writes the notice file. When the server does not know
// the lease, beat gets a new lease and sends the heartbeat again. It returns an error only
// when l ends (endsLease). Each other failure does not stop the bridge: the next call or
// heartbeat tries again.
func (b *Bridge) beat(ctx context.Context, l *lease) error {
	token := l.current()
	out, err := b.heartbeat(ctx, token)
	if ctx.Err() != nil {
		return nil
	}
	if isLeaseUnknown(err) {
		b.logf("the server does not know the lease of %s; the bridge asks for a new lease", l.describe())
		if rerr := b.renew(ctx, l, token); rerr != nil {
			if ctx.Err() != nil {
				// A logout or the stop of the bridge ends l.
				return nil
			}
			if endsLease(l, rerr) {
				return rerr
			}
			b.logf("the new lease failed: %v. The next call or heartbeat tries again.", rerr)
			return nil
		}
		token = l.current()
		out, err = b.heartbeat(ctx, token)
		if ctx.Err() != nil {
			return nil
		}
	}
	if err != nil {
		// A network error or a server error does not stop the bridge. The next heartbeat
		// tries again.
		b.logf("the heartbeat failed: %v", err)
		return nil
	}
	b.record(l, token, out)
	return nil
}

// record keeps the session and the count of new mail of a heartbeat answer, and writes the
// notice file. A pool lease that gets a session after a bind records it.
func (b *Bridge) record(l *lease, token string, out beatAnswer) {
	l.mu.Lock()
	if l.ended || l.token != token {
		l.mu.Unlock()
		return
	}
	before := l.login
	switch {
	case out.Session != nil && *out.Session != "":
		l.setLogin(*out.Session)
	case l.handle == "":
		l.setLogin("")
	}
	if out.NewMail != nil {
		l.newMail, l.updatedAt = *out.NewMail, time.Now().UTC().Format(time.RFC3339)
	}
	after := l.login
	l.mu.Unlock()
	if l.handle == "" && after != before {
		if after == "" {
			b.logNoPool()
		} else {
			b.logf("the pool lease has the session %s", after)
		}
	}
	b.writeNotice()
}

// drop removes a named lease that ended on the server and that the bridge cannot renew.
// A subsequent tool call with its handle gets SESSION_UNKNOWN.
func (b *Bridge) drop(l *lease, why error) {
	if b.namedLease(l.handle) == l {
		b.forget(l.handle)
	}
	l.finish()
	l.cancel()
	b.logf("the session %s ended: %v", l.session(), why)
	b.writeNotice()
}

// leaseEndTimeout is the longest time that the bridge waits for the end of one lease. The
// server ends the mail login of the lease before it answers: the logout can take 3 s, and
// the close of the mail connections 2 s.
const leaseEndTimeout = 10 * time.Second

// deleteLease ends one lease on the server.
func (b *Bridge) deleteLease(token string) {
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), leaseEndTimeout)
	defer cancel()
	if err := b.callJSON(ctx, http.MethodDelete, "/api/leases/current", token, nil, nil); err != nil && !isLeaseUnknown(err) {
		b.logf("the end of the lease failed: %v", err)
	}
}

// leases gives the pool lease and the named leases, sorted by login.
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
// 0700. The file has the pool session and each named session, sorted by login, and holds
// no secret. While the pool lease has no session, the file has no pool entry. When the
// bridge has no session, it removes the file.
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
		n = hook.Notice{}
	}
	for _, l := range named {
		if e := l.notice(); e.Session != "" && !l.isEnded() {
			n.Named = append(n.Named, e)
		}
	}
	if n.Session == "" && len(n.Named) == 0 {
		if err := os.Remove(b.noticePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			b.logf("the removal of the notice file failed: %v", err)
		}
		return
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
	// The bridge ends the leases at the same time, each with its own wait. Thus a slow
	// end of one lease does not stop the end of the other leases.
	var ends sync.WaitGroup
	for _, l := range all {
		token := l.finish()
		if token == "" {
			continue
		}
		ends.Add(1)
		go func(l *lease, token string) {
			defer ends.Done()
			ctx, cancel := context.WithTimeout(context.Background(), leaseEndTimeout)
			defer cancel()
			if err := b.callJSON(ctx, http.MethodDelete, "/api/leases/current", token, nil, nil); err != nil && !isLeaseUnknown(err) {
				b.logf("the end of the lease of %s failed: %v", l.describe(), err)
			}
		}(l, token)
	}
	ends.Wait()
}
