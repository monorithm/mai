package bridge

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// The tools of the bridge. The bridge answers them itself. A call of one of them that
// reaches the server gets BRIDGE_ONLY.
const (
	toolLogin  = "login"
	toolLogout = "logout"
)

// argSession is the argument of each tool call that selects the session of the call.
const argSession = "session"

// handleRule is the form of a handle: "ses-" and 16 characters of [a-z2-7].
var handleRule = regexp.MustCompile(`^ses-[a-z2-7]{16}$`)

// labelRule is the form of the label of a session. It is the label rule of the server.
var labelRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// loginRule is the form of the login of a session: an agent name, a dot, and a label. The
// agent name has the agent name rule of the server.
var loginRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}\.[a-z0-9][a-z0-9-]{0,31}$`)

// isSessionName is true when s has the form of a label or of a login. A value with the
// form of a handle is always a handle, also when it has the form of a label.
func isSessionName(s string) bool {
	return !handleRule.MatchString(s) && (labelRule.MatchString(s) || loginRule.MatchString(s))
}

// handleEncoding gives 16 characters of [a-z2-7] for 10 random bytes.
var handleEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newHandle makes a handle from 80 random bits. A handle is valid only in this bridge, and
// it is not a secret of the server.
func newHandle() (string, error) {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "ses-" + handleEncoding.EncodeToString(raw[:]), nil
}

// toolCall is the part of a tools/call request that the bridge reads.
type toolCall struct {
	msg     map[string]json.RawMessage
	params  map[string]json.RawMessage
	name    string
	args    map[string]json.RawMessage
	badArgs bool // The arguments are not an object.
}

// parseToolCall reads a tools/call request. It returns false for each other message.
func parseToolCall(line []byte) (toolCall, bool) {
	var c toolCall
	if json.Unmarshal(line, &c.msg) != nil {
		return c, false
	}
	var method string
	if json.Unmarshal(c.msg["method"], &method) != nil || method != "tools/call" {
		return c, false
	}
	if json.Unmarshal(c.msg["params"], &c.params) != nil || c.params == nil {
		return c, false
	}
	if json.Unmarshal(c.params["name"], &c.name) != nil {
		return c, false
	}
	if raw, ok := c.params["arguments"]; ok && string(raw) != "null" {
		if json.Unmarshal(raw, &c.args) != nil {
			c.args, c.badArgs = nil, true
		}
	}
	return c, true
}

// session gives the argument session. given is false when the call has no value: no
// argument, null, or an empty string. text is false when the value is not a string. A value
// that is not a string is never a handle, a label, or a login.
func (c toolCall) session() (value string, given, text bool) {
	raw, ok := c.args[argSession]
	if !ok || string(raw) == "null" {
		return "", false, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return string(raw), true, false
	}
	return s, s != "", true
}

// without gives the line of the call with no argument session.
func (c toolCall) without() ([]byte, error) {
	args := make(map[string]json.RawMessage, len(c.args))
	for k, v := range c.args {
		if k != argSession {
			args[k] = v
		}
	}
	a, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	params := make(map[string]json.RawMessage, len(c.params))
	for k, v := range c.params {
		params[k] = v
	}
	params["arguments"] = a
	p, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	msg := make(map[string]json.RawMessage, len(c.msg))
	for k, v := range c.msg {
		msg[k] = v
	}
	msg["params"] = p
	return json.Marshal(msg)
}

// toolResult is the result of a tool call, in the same form as a result of the server.
func toolResult(structured map[string]any, isError bool) map[string]any {
	text, _ := json.Marshal(structured)
	r := map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(text)}},
		"structuredContent": structured,
	}
	if isError {
		r["isError"] = true
	}
	return r
}

// toolError is a tool result with isError true, the code of the error, and retry_safe
// false.
func toolError(code, message string) map[string]any { return sessionError(code, message, false) }

// sessionError is a tool result with isError true, the code of the error, and the path
// session. With retrySafe true, the chat can try the same call again after some time.
func sessionError(code, message string, retrySafe bool) map[string]any {
	return failure(code, message, argSession, retrySafe)
}

// failure is a tool result with isError true, the code of the error, and the path: argSession,
// or nil for a call with no argument session.
func failure(code, message string, path any, retrySafe bool) map[string]any {
	return toolResult(map[string]any{"error": map[string]any{
		"code": code, "message": message, "path": path, "retry_safe": retrySafe,
	}}, true)
}

// loginError is the tool error of a login when the lease request is not successful. A tool
// call with the label or the login of a session gets the same tool error. Each answer with
// an error code gives that code.
//
// An HTTP 5xx answer, an answer with no code, or a network error gives retry_safe true, also
// when the server gives retry_safe false. This type of failure is a failure of the server,
// not of the session, and a login changes no data. Thus it is safe for the chat to try the
// login again after some time. A tool call with a handle gets the same retry_safe for the
// same failure.
//
// An HTTP 4xx answer, for example SESSION_BUSY, gives the retry_safe of the server, or false
// when the answer has no retry_safe.
//
// A failed read of the agent key is an error of the host (keyFailure): KEY_FILE_ERROR, or BUSY
// for a key file that is busy.
func loginError(agent string, err error) map[string]any {
	var ae *apiError
	if code, msg, safe, ok := keyFailure(agent, err); ok {
		return sessionError(code, msg, safe)
	}
	switch {
	case errors.Is(err, errNoKey):
		return toolError("BAD_KEY", noKeyMessage(agent))
	case errors.As(err, &ae) && ae.Code != "":
		safe := ae.Status >= 500
		if !safe && ae.RetrySafe != nil {
			safe = *ae.RetrySafe
		}
		return sessionError(ae.Code, ae.Message, safe)
	}
	return sessionError("SERVER_UNAVAILABLE", "The login got no lease: "+err.Error()+". Try again later.", true)
}

// msgUnknownHandle is the message of SESSION_UNKNOWN for a handle that the bridge does not
// hold.
const msgUnknownHandle = "The session handle is not known in this bridge. " +
	"Give the label of the session as session, or call login again."

// msgEnded is the message of SESSION_UNKNOWN when the bridge ended the lease of the session
// during the call, for example at a logout.
const msgEnded = "The lease of the session ended in this bridge, for example at a logout. " +
	"Give the label of the session as session to get a new lease, or call login again."

// msgBadSession is the message of INVALID_ARGUMENT for a value of the argument session that
// is not a handle, a label, or a login.
const msgBadSession = "Give the label or the login of a named session as session, for example lane-1, " +
	"or the handle that login gave."

// msgNoLease is the message of SESSION_UNKNOWN for a logout of a label or a login that has
// no lease in this bridge.
func msgNoLease(login string) string {
	return "This bridge has no lease for the session " + login + "."
}

func (b *Bridge) writeResult(id json.RawMessage, result any) {
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	b.writeLine(line)
}

// namedLease gives the lease of a handle, or nil.
func (b *Bridge) namedLease(handle string) *lease {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.named[handle]
}

// loginOf gives the login of the name of a session: a login stays, and a label gets the
// agent of the bridge, for example cursor.lane-1 for lane-1.
func (b *Bridge) loginOf(name string) string {
	if strings.Contains(name, ".") {
		return name
	}
	return b.Agent + "." + name
}

// leaseOfLogin gives the live named lease of a login, or nil.
func (b *Bridge) leaseOfLogin(login string) *lease {
	b.mu.Lock()
	l := b.named[b.byLogin[login]]
	b.mu.Unlock()
	if l == nil || l.isEnded() {
		return nil
	}
	return l
}

// forget removes the handle from the maps of the bridge and returns its lease, or nil.
func (b *Bridge) forget(handle string) *lease {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.named[handle]
	delete(b.named, handle)
	for k, h := range b.byLogin {
		if h == handle {
			delete(b.byLogin, k)
		}
	}
	return l
}

// sessionResult is the result of login and logout.
func (b *Bridge) sessionResult(handle, login string) map[string]any {
	return toolResult(map[string]any{"session": handle, "login": login, "agent": b.Agent}, false)
}

// opening is an open of one session that runs. The other calls for the same session wait for
// done, and then use l or fail.
type opening struct {
	done chan struct{}
	l    *lease
	fail map[string]any
}

// open gives the named lease of the session name, a label or a login of the agent of the
// bridge. When the bridge has no lease for that session, open sends one lease request for
// it, and keeps the lease with a new handle and a heartbeat loop. When the request is not
// successful, fail is the tool error of the call, with the path session.
//
// Calls at the same time for the same session make one lease request. Each of them gets the
// result of that request: the same lease, or the same tool error. Thus a server that does
// not reply does not make each call wait for the lease request of each call before it.
func (b *Bridge) open(ctx context.Context, name string) (*lease, map[string]any) {
	login := b.loginOf(name)
	for {
		b.mu.Lock()
		o := b.opens[login]
		if o == nil {
			if b.opens == nil {
				b.opens = map[string]*opening{}
			}
			o = &opening{done: make(chan struct{})}
			b.opens[login] = o
			b.mu.Unlock()
			o.l, o.fail = b.openOnce(ctx, name, login)
			b.mu.Lock()
			delete(b.opens, login)
			b.mu.Unlock()
			close(o.done)
			return o.l, o.fail
		}
		b.mu.Unlock()
		<-o.done
		if o.fail != nil {
			return nil, o.fail
		}
		if !o.l.isEnded() {
			return o.l, nil
		}
		// A logout ended the lease after the open. Get a new lease, as the first call does.
	}
}

// openOnce does the steps of open for the login of name. One openOnce or logout at a time
// (loginMu). Thus an open waits until a logout of the same session ends the lease, and then
// it does not get SESSION_BUSY from that lease.
func (b *Bridge) openOnce(ctx context.Context, name, login string) (l *lease, fail map[string]any) {
	b.loginMu.Lock()
	defer b.loginMu.Unlock()
	b.mu.Lock()
	l = b.named[b.byLogin[login]]
	b.mu.Unlock()
	if l != nil && !l.isEnded() {
		return l, nil
	}
	a, err := b.request(ctx, name)
	if err != nil {
		return nil, loginError(b.Agent, err)
	}
	if a.Session == "" {
		a.Session = login
	}
	handle, err := newHandle()
	if err != nil {
		b.deleteLease(a.Lease)
		return nil, sessionError("INTERNAL", "The bridge got no random handle: "+err.Error()+". Try again later.", true)
	}
	b.mu.Lock()
	for b.named[handle] != nil {
		handle, _ = newHandle()
	}
	l = b.newLease(handle, a)
	b.named[handle] = l
	b.byLogin[a.Session] = handle
	b.byLogin[login] = handle
	b.mu.Unlock()
	b.startBeats(l)
	b.logf("a chat logged in to the session %s", a.Session)
	return l, nil
}

// login answers the tool login. It gets a lease for the named session of the argument
// session, which is a label or a login of the agent of the bridge, and gives a handle. A
// second login to a session that the bridge holds gives the same handle, also when a tool
// call with the label of the session got the lease.
func (b *Bridge) login(ctx context.Context, id json.RawMessage, c toolCall) {
	var name string
	ok := json.Unmarshal(c.args[argSession], &name) == nil
	name = strings.TrimSpace(name)
	if c.badArgs || !ok || name == "" {
		b.writeResult(id, toolError("BAD_LABEL", "Give the label or the login of a session as the argument session."))
		return
	}
	l, fail := b.open(ctx, name)
	if fail != nil {
		b.writeResult(id, fail)
		return
	}
	b.writeResult(id, b.sessionResult(l.handle, l.session()))
}

// logout answers the tool logout. The argument session is a handle, or the label or the
// login of a named session. The logout ends the heartbeat loop and the lease of that
// session, and then removes the handle. A login or a call with the label waits for the
// logout (loginMu). Thus it never gets SESSION_BUSY from the lease that the logout ends.
//
// When the heartbeat loop waits for the answer of a lease request, the logout waits for that
// answer, and then ends the new lease also (request).
func (b *Bridge) logout(id json.RawMessage, c toolCall) {
	value, given, text := c.session()
	if given && (!text || !handleRule.MatchString(value) && !isSessionName(value)) {
		b.writeResult(id, toolError("INVALID_ARGUMENT", msgBadSession))
		return
	}
	b.loginMu.Lock()
	defer b.loginMu.Unlock()
	handle, missing := value, msgUnknownHandle
	if isSessionName(value) {
		login := b.loginOf(value)
		b.mu.Lock()
		handle = b.byLogin[login]
		b.mu.Unlock()
		missing = msgNoLease(login)
	}
	var l *lease
	if handle != "" {
		l = b.namedLease(handle)
	}
	if l == nil {
		b.writeResult(id, toolError("SESSION_UNKNOWN", missing))
		return
	}
	l.cancel()
	<-l.done
	token := l.finish()
	l.waitRenew()
	b.deleteLease(token)
	if b.namedLease(handle) == l {
		b.forget(handle)
	}
	b.writeNotice()
	b.logf("a chat logged out of the session %s", l.session())
	b.writeResult(id, b.sessionResult(handle, l.session()))
}

// route selects the lease of a line. It answers login and logout itself, and then returns
// false. For a tool call with the argument session, it gives the lease of that session
// (leaseOf) and the line with no argument session. isCall is true for a tools/call. name is
// the label or the login of the argument session, or "".
func (b *Bridge) route(ctx context.Context, id json.RawMessage, hasID bool, line []byte) (l *lease, out []byte, isCall bool, name string, ok bool) {
	b.mu.Lock()
	pool := b.pool
	b.mu.Unlock()
	if !hasID {
		return pool, line, false, "", true
	}
	c, ok := parseToolCall(line)
	if !ok {
		return pool, line, false, "", true
	}
	switch c.name {
	case toolLogin:
		b.login(ctx, id, c)
		return nil, nil, true, "", false
	case toolLogout:
		b.logout(id, c)
		return nil, nil, true, "", false
	}
	if _, has := c.args[argSession]; !has {
		return pool, line, true, "", true
	}
	if l = b.leaseOf(ctx, id, c, pool); l == nil {
		return nil, nil, true, "", false
	}
	if value, given, text := c.session(); given && text && isSessionName(value) {
		name = value
	}
	out, err := c.without()
	if err != nil {
		b.writeError(id, -32603, "mai: "+err.Error())
		return nil, nil, true, "", false
	}
	return l, out, true, name, true
}

// leaseOf gives the lease of the argument session of a tool call that is not login or
// logout:
//   - With no value, the pool lease.
//   - With a handle, the lease of that handle.
//   - With a label or a login of a session, the named lease of that session. When the bridge
//     has no such lease, the call gets it as a login does (open). Thus a chat needs no login.
//
// When the call gets no lease, leaseOf writes the tool error of the call and returns nil. A
// value that is not a handle, a label, or a login gets INVALID_ARGUMENT.
func (b *Bridge) leaseOf(ctx context.Context, id json.RawMessage, c toolCall, pool *lease) *lease {
	value, given, text := c.session()
	switch {
	case !given:
		return pool
	case text && handleRule.MatchString(value):
		if l := b.namedLease(value); l != nil && !l.isEnded() {
			return l
		}
		b.writeResult(id, toolError("SESSION_UNKNOWN", msgUnknownHandle))
		return nil
	case text && isSessionName(value):
		if l := b.leaseOfLogin(b.loginOf(value)); l != nil {
			return l
		}
		l, fail := b.open(ctx, value)
		if fail != nil {
			b.writeResult(id, fail)
			return nil
		}
		return l
	}
	b.writeResult(id, toolError("INVALID_ARGUMENT", msgBadSession))
	return nil
}
