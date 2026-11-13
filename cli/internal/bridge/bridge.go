// Package bridge is "mai mcp": it gets a lease for an agent and bridges the MCP stdio of an
// app to POST /mcp of the server.
//
// The bridge holds the pool lease of the agent, and one lease for each named session of its
// chats. A chat gives the label or the login of its session as the argument session of each
// tool call, for example lane-1. The first call with the label gets the lease of that
// session, as the tool login does. The tool login is optional: it gets the lease and gives
// a handle, which a chat can give as session in place of the label. The bridge removes the
// argument and sends the call with the lease of that session. A tool call with no argument
// session uses the pool lease.
//
// When a lease ends on the server, the bridge asks for a new lease. Only a refusal of the
// server (an HTTP 4xx answer with an error code) ends a named session in the bridge. A
// server error, an answer with no code, or a network error keeps the session, and the next
// call or heartbeat tries again.
//
// Each lease token is only in the memory of the bridge. It is never in a file, an argument,
// or the environment. Stdout carries only JSON-RPC lines. Logs go to stderr.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Exit codes of Run.
const (
	ExitOK    = 0
	ExitError = 1
)

// maxLine is the largest JSON-RPC line that the bridge reads from the app.
const maxLine = 64 << 20

// maxInFlight is the largest number of requests that the bridge sends at the same time. A
// long wait of one chat thus does not stop the tool calls of a different chat.
const maxInFlight = 32

// KeySource gives the key of an account. The key file of the CLI (keys.File) is one.
type KeySource interface {
	Get(account string) (string, error)
}

// Bridge holds the state of one bridge process. Set the exported fields, then call Run.
type Bridge struct {
	Agent    string
	Instance string
	Endpoint string    // The base URL of the server, for example http://127.0.0.1:8765.
	Keys     KeySource // The bridge reads the agent key at each lease request.
	Client   *http.Client
	StateDir string
	App      string // The app at the top of the process chain.
	AppPID   int    // The parent PID of the bridge. The notice file has this PID in its name.
	PID      int    // The PID of the bridge.
	Folder   string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer

	// requestMu lets one lease request occur at a time.
	requestMu sync.Mutex

	// mu guards pool, named, byLogin, and opens.
	mu      sync.Mutex
	pool    *lease
	named   map[string]*lease   // handle -> lease
	byLogin map[string]string   // login -> handle
	opens   map[string]*opening // login -> the open that runs

	loginMu     sync.Mutex
	noticeMu    sync.Mutex
	noticesGone bool
	outMu       sync.Mutex

	beatCtx context.Context
	beats   sync.WaitGroup
	fatal   chan error
}

// apiError is an error answer of the server. RetrySafe is nil when the answer does not
// have retry_safe.
type apiError struct {
	Status    int
	Code      string
	Message   string
	RetrySafe *bool
}

func (e *apiError) Error() string {
	if e.Code == "" {
		return "HTTP " + strconv.Itoa(e.Status)
	}
	return e.Code + ": " + e.Message
}

func isLeaseUnknown(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized && ae.Code == "LEASE_UNKNOWN"
}

func readAPIError(resp *http.Response, body []byte) *apiError {
	ae := &apiError{Status: resp.StatusCode}
	var e struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RetrySafe *bool  `json:"retry_safe"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		ae.Code, ae.Message, ae.RetrySafe = e.Error.Code, e.Error.Message, e.Error.RetrySafe
	}
	return ae
}

func (b *Bridge) logf(format string, args ...any) {
	if b.Stderr != nil {
		fmt.Fprintf(b.Stderr, "mai mcp: "+format+"\n", args...)
	}
}

// callJSON sends one API call with a bearer token and decodes a 2xx answer into out.
func (b *Bridge) callJSON(ctx context.Context, method, path, bearer string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.Endpoint+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := b.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return readAPIError(resp, data)
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (b *Bridge) writeLine(line []byte) {
	b.outMu.Lock()
	defer b.outMu.Unlock()
	_, _ = b.Stdout.Write(append(line, '\n'))
}

func (b *Bridge) writeError(id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	line, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
	b.writeLine(line)
}

// requestID returns the id of a request, or nil for a notification.
func requestID(line []byte) (json.RawMessage, bool, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		var arr []json.RawMessage
		if json.Unmarshal(line, &arr) == nil {
			return nil, true, nil
		}
		return nil, false, err
	}
	id, ok := m["id"]
	if !ok || string(id) == "null" {
		return nil, false, nil
	}
	return id, true, nil
}

// errStop means that the bridge cannot continue.
var errStop = errors.New("the bridge stops")

// handle sends one JSON-RPC line to /mcp with the lease of its session and writes the
// answer. It answers the tools login and logout itself.
func (b *Bridge) handle(ctx context.Context, line []byte) error {
	id, hasID, err := requestID(line)
	if err != nil {
		b.writeError(nil, -32700, "parse error")
		return nil
	}
	l, line, isCall, name, ok := b.route(ctx, id, hasID, line)
	if !ok {
		return nil
	}
	if isCall && l.handle == "" {
		// The call can bind a pool session to the pool lease, also when its answer is an
		// error. A renew during the call can also give the pool lease no session. The
		// heartbeat gives the session of a bind, thus the notice file gets the pool entry at
		// once.
		unbound := l.session() == ""
		defer func() {
			if unbound || l.session() == "" {
				l.beatSoon()
			}
		}()
	}
	token := l.current()
	answer, err := b.post(ctx, token, line)
	if isLeaseUnknown(err) {
		b.logf("the server does not know the lease of %s; the bridge asks for a new lease", l.describe())
		rerr := b.renew(ctx, l, token)
		if errors.Is(rerr, errEnded) && name != "" {
			// The bridge ended the lease during the call, for example at a logout of the
			// session. The server did not do the call. A call with the label or the login
			// of a session gets a new lease for it, as the first call does.
			next, fail := b.open(ctx, name)
			if fail != nil {
				b.writeResult(id, fail)
				return nil
			}
			l, rerr = next, nil
		}
		if rerr != nil {
			return b.renewFailed(id, hasID, isCall, l, rerr)
		}
		answer, err = b.post(ctx, l.current(), line)
		if isLeaseUnknown(err) {
			// The server ended the new lease immediately. The lease stays in the bridge, thus
			// the next call or heartbeat gets a new lease.
			if hasID {
				b.answerFailure(id, isCall, "SERVER_UNAVAILABLE",
					"The server ended the new lease of "+l.describe()+" immediately. Try again later.", true)
			}
			return nil
		}
	}
	if err != nil {
		if hasID {
			b.writeError(id, -32603, "mai: "+err.Error())
		}
		return nil
	}
	if !hasID || len(answer) == 0 {
		return nil
	}
	for _, a := range answer {
		b.writeLine(a)
	}
	return nil
}

// renewFailed answers a call when the lease of its session ended and the renew failed.
//
// Only an agent key that is not valid stops the bridge. A named session that the server
// refused (isRefusal) goes, and the call gets the code of the server. For each other
// failure, the lease stays in the bridge, and the call gets a tool error with retry_safe
// true. The next call or heartbeat tries again.
func (b *Bridge) renewFailed(id json.RawMessage, hasID, isCall bool, l *lease, err error) error {
	switch {
	case errors.Is(err, errEnded):
		if hasID {
			b.answerFailure(id, isCall, "SESSION_UNKNOWN", msgEnded, false)
		}
		return nil
	case l.handle == "" && isBadKey(err):
		b.writeError(id, -32001, stopText(b.Agent, err))
		return errStop
	case l.handle != "" && isRefusal(err):
		b.drop(l, err)
		if hasID {
			code, msg := refusal(b.Agent, err)
			b.writeResult(id, toolError(code, "The session "+l.session()+" ended: "+msg))
		}
		return nil
	}
	b.logf("the new lease for %s failed: %v. The next call or heartbeat tries again.", l.describe(), err)
	if hasID {
		code, msg := unavailable(l, err)
		b.answerFailure(id, isCall, code, msg, true)
	}
	return nil
}

// answerFailure answers a request with an error: a tool call gets a tool error, and a
// different request gets a JSON-RPC error.
func (b *Bridge) answerFailure(id json.RawMessage, isCall bool, code, message string, retrySafe bool) {
	if isCall {
		b.writeResult(id, sessionError(code, message, retrySafe))
		return
	}
	b.writeError(id, -32603, "mai: "+code+": "+message)
}

// refusal gives the code and the message of a refused lease request.
func refusal(agent string, err error) (string, string) {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Code, ae.Message
	}
	return "BAD_KEY", noKeyMessage(agent)
}

// stopText is the text of the error that stops the bridge, when the agent key is not valid.
func stopText(agent string, err error) string {
	code, msg := refusal(agent, err)
	return "mai: the lease ended, and the agent key is not valid: " + code + ": " + msg + " The bridge stops."
}

// post sends one line to /mcp and returns the answer lines.
func (b *Bridge) post(ctx context.Context, lease string, line []byte) ([][]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.Endpoint+"/mcp", bytes.NewReader(line))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+lease)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLine))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusAccepted {
		return nil, nil
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return sseData(data), nil
	}
	trimmed := bytes.TrimSpace(data)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if isJSONRPC(trimmed) {
			return compact(trimmed), nil
		}
		return nil, readAPIError(resp, data)
	}
	if len(trimmed) == 0 {
		return nil, nil
	}
	out := compact(trimmed)
	if out == nil {
		return nil, errors.New("the server gave an answer that is not JSON")
	}
	return out, nil
}

func isJSONRPC(data []byte) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	_, ok := m["jsonrpc"]
	return ok
}

func compact(data []byte) [][]byte {
	var buf bytes.Buffer
	if json.Compact(&buf, data) != nil {
		return nil
	}
	return [][]byte{buf.Bytes()}
}

// sseData returns the data of each event of a text/event-stream body as one line.
func sseData(data []byte) [][]byte {
	var out [][]byte
	var cur []byte
	flush := func() {
		if len(cur) > 0 {
			out = append(out, compact(cur)...)
			cur = nil
		}
	}
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case l == "":
			flush()
		case strings.HasPrefix(l, "data:"):
			cur = append(cur, strings.TrimPrefix(strings.TrimPrefix(l, "data:"), " ")...)
		}
	}
	flush()
	return out
}

// Run gets the pool lease, bridges stdin to /mcp until stdin ends or ctx ends, and then
// ends each lease of the bridge. Cancel ctx on SIGINT, SIGTERM, or SIGHUP.
//
// The pool lease can have no session, when no pool session of the agent is free. The bridge
// then serves the requests that use no session and the named sessions. When the start
// gets no lease, Run stops. After the start, only an agent key that is not valid stops Run.
func (b *Bridge) Run(ctx context.Context) int {
	if err := harden(); err != nil {
		b.logf("the process protection failed: %v", err)
		return ExitError
	}
	if b.Client == nil {
		b.Client = &http.Client{Transport: &http.Transport{Proxy: nil}}
	}
	b.mu.Lock()
	b.named, b.byLogin = map[string]*lease{}, map[string]string{}
	b.mu.Unlock()
	b.fatal = make(chan error, 1)
	a, err := b.request(ctx, "")
	if err != nil {
		if errors.Is(err, errNoKey) {
			fmt.Fprintf(b.Stderr, "mai mcp: the key file has no key for the agent %s. Run \"mai agent add %s\".\n", b.Agent, b.Agent)
		} else {
			b.logf("no lease for the agent %s: %v", b.Agent, err)
		}
		return ExitError
	}
	if a.Session == "" {
		b.logNoPool()
	} else {
		b.logf("the agent %s has the session %s", b.Agent, a.Session)
	}

	ctx, cancel := context.WithCancel(ctx)
	b.beatCtx = ctx
	pool := b.newLease("", a)
	b.mu.Lock()
	b.pool = pool
	b.mu.Unlock()
	b.startBeats(pool)
	var inflight sync.WaitGroup
	stop := make(chan struct{}, 1)
	// Stop each request and each heartbeat before the end of the leases. Then no heartbeat
	// writes the notice file again after its removal.
	defer func() {
		cancel()
		inflight.Wait()
		b.beats.Wait()
		b.endAll()
	}()

	lines := make(chan []byte)
	go func() {
		defer close(lines)
		r := bufio.NewReaderSize(b.Stdin, 64<<10)
		for {
			line, err := readLine(r)
			if len(bytes.TrimSpace(line)) > 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	slots := make(chan struct{}, maxInFlight)
	for {
		select {
		case <-ctx.Done():
			return ExitOK
		case err := <-b.fatal:
			b.writeError(nil, -32001, stopText(b.Agent, err))
			b.logf("stop: %v", err)
			return ExitError
		case <-stop:
			return ExitError
		case line, ok := <-lines:
			if !ok {
				// The app closed stdin. Each request that started gets its answer first.
				inflight.Wait()
				select {
				case <-stop:
					return ExitError
				default:
					return ExitOK
				}
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return ExitOK
			}
			inflight.Add(1)
			go func() {
				defer inflight.Done()
				defer func() { <-slots }()
				if err := b.handle(ctx, line); err != nil {
					select {
					case stop <- struct{}{}:
					default:
					}
				}
			}()
		}
	}
}

// readLine reads one line of any length up to maxLine.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, isPrefix, err := r.ReadLine()
		line = append(line, part...)
		if err != nil {
			return line, err
		}
		if len(line) > maxLine {
			return nil, errors.New("the line is too long")
		}
		if !isPrefix {
			return line, nil
		}
	}
}
