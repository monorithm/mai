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

// session gives the argument session, and true when the call has a value. A null value
// or an empty string counts as no value. A value that is not a string is never a handle.
func (c toolCall) session() (string, bool) {
	raw, ok := c.args[argSession]
	if !ok || string(raw) == "null" {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return string(raw), true
	}
	return s, s != ""
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
	return toolResult(map[string]any{"error": map[string]any{
		"code": code, "message": message, "path": argSession, "retry_safe": retrySafe,
	}}, true)
}

// loginError is the tool error of a login when the lease request is not successful. Each
// answer with an error code gives that code.
//
// An HTTP 5xx answer, an answer with no code, or a network error gives retry_safe true, also
// when the server gives retry_safe false. This type of failure is a failure of the server,
// not of the session, and a login changes no data. Thus it is safe for the chat to try the
// login again after some time. A tool call with a handle gets the same retry_safe for the
// same failure.
//
// An HTTP 4xx answer, for example SESSION_BUSY, gives the retry_safe of the server, or false
// when the answer has no retry_safe.
func loginError(agent string, err error) map[string]any {
	var ae *apiError
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
const msgUnknownHandle = "The session handle is not known in this bridge. Call login again."

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

// login answers the tool login. It gets a lease for the named session of the argument
// session, which is a label or a login of the agent of the bridge, and gives a handle. A
// second login to a session that the bridge holds gives the same handle.
func (b *Bridge) login(ctx context.Context, id json.RawMessage, c toolCall) {
	var name string
	ok := json.Unmarshal(c.args[argSession], &name) == nil
	name = strings.TrimSpace(name)
	if c.badArgs || !ok || name == "" {
		b.writeResult(id, toolError("BAD_LABEL", "Give the label or the login of a session as the argument session."))
		return
	}
	login := name
	if !strings.Contains(name, ".") {
		login = b.Agent + "." + name
	}
	// One login or logout at a time. Thus two chats that log in to the same session get one
	// lease. A login also waits until a logout of the same session ends the lease.
	b.loginMu.Lock()
	defer b.loginMu.Unlock()
	b.mu.Lock()
	h, held := b.byLogin[login]
	b.mu.Unlock()
	if held {
		b.writeResult(id, b.sessionResult(h, login))
		return
	}
	a, err := b.request(ctx, name)
	if err != nil {
		b.writeResult(id, loginError(b.Agent, err))
		return
	}
	if a.Session == "" {
		a.Session = login
	}
	handle, err := newHandle()
	if err != nil {
		b.deleteLease(a.Lease)
		b.writeError(id, -32603, "mai: no random handle: "+err.Error())
		return
	}
	b.mu.Lock()
	for b.named[handle] != nil {
		handle, _ = newHandle()
	}
	l := b.newLease(handle, a)
	b.named[handle] = l
	b.byLogin[a.Session] = handle
	b.byLogin[login] = handle
	b.mu.Unlock()
	b.startBeats(l)
	b.logf("a chat logged in to the session %s", a.Session)
	b.writeResult(id, b.sessionResult(handle, a.Session))
}

// logout answers the tool logout. It ends the heartbeat loop and the lease of the handle of
// the argument session, and then removes the handle. A login waits for the logout (loginMu).
// Thus a login to the same session never gets SESSION_BUSY from the lease that the logout
// ends.
//
// When the heartbeat loop waits for the answer of a lease request, the logout waits for that
// answer, and then ends the new lease also (request).
func (b *Bridge) logout(id json.RawMessage, c toolCall) {
	handle, _ := c.session()
	b.loginMu.Lock()
	defer b.loginMu.Unlock()
	var l *lease
	if handleRule.MatchString(handle) {
		l = b.namedLease(handle)
	}
	if l == nil {
		b.writeResult(id, toolError("SESSION_UNKNOWN", msgUnknownHandle))
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
// false. For a tool call with the argument session, it gives the lease of the handle and
// the line with no argument session. isCall is true for a tools/call.
func (b *Bridge) route(ctx context.Context, id json.RawMessage, hasID bool, line []byte) (l *lease, out []byte, isCall, ok bool) {
	b.mu.Lock()
	pool := b.pool
	b.mu.Unlock()
	if !hasID {
		return pool, line, false, true
	}
	c, ok := parseToolCall(line)
	if !ok {
		return pool, line, false, true
	}
	switch c.name {
	case toolLogin:
		b.login(ctx, id, c)
		return nil, nil, true, false
	case toolLogout:
		b.logout(id, c)
		return nil, nil, true, false
	}
	if _, has := c.args[argSession]; !has {
		return pool, line, true, true
	}
	l = pool
	if handle, given := c.session(); given {
		l = nil
		if handleRule.MatchString(handle) {
			l = b.namedLease(handle)
		}
		if l == nil || l.isEnded() {
			b.writeResult(id, toolError("SESSION_UNKNOWN", msgUnknownHandle))
			return nil, nil, true, false
		}
	}
	out, err := c.without()
	if err != nil {
		b.writeError(id, -32603, "mai: "+err.Error())
		return nil, nil, true, false
	}
	return l, out, true, true
}
