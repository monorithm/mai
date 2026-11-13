// Package bridge is "mai mcp": it gets a lease for an agent and bridges the MCP stdio of an
// app to POST /mcp of the server.
//
// The bridge holds the pool lease of the agent, and one lease for each named session that
// a chat logged in to with the tool login. The chat gives the handle of the login as the
// argument session of each tool call. The bridge removes the argument and sends the call
// with the lease of that handle. A tool call with no argument session uses the pool lease.
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

	"github.com/monorithm/mai/cli/v3/internal/keychain"
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

// Bridge holds the state of one bridge process. Set the exported fields, then call Run.
type Bridge struct {
	Agent    string
	Instance string
	Endpoint string // The base URL of the server, for example http://127.0.0.1:8765.
	Store    keychain.Store
	Presence func(reason string) error
	Client   *http.Client
	StateDir string
	App      string // The app at the top of the process chain.
	AppPID   int    // The parent PID of the bridge. The notice file has this PID in its name.
	PID      int    // The PID of the bridge.
	Folder   string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer

	// approveMu lets one lease request occur at a time. approved is true after the
	// presence check of this process.
	approveMu sync.Mutex
	approved  bool

	// mu guards pool, named, and byLogin.
	mu      sync.Mutex
	pool    *lease
	named   map[string]*lease // handle -> lease
	byLogin map[string]string // login -> handle

	loginMu     sync.Mutex
	noticeMu    sync.Mutex
	noticesGone bool
	outMu       sync.Mutex

	beatCtx context.Context
	beats   sync.WaitGroup
	fatal   chan error
}

// apiError is an error answer of the server.
type apiError struct {
	Status  int
	Code    string
	Message string
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
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		ae.Code, ae.Message = e.Error.Code, e.Error.Message
	}
	return ae
}

func (b *Bridge) logf(format string, args ...any) {
	if b.Stderr != nil {
		fmt.Fprintf(b.Stderr, "mai mcp: "+format+"\n", args...)
	}
}

// Reason is the text of the presence prompt for the first lease of the bridge.
func (b *Bridge) Reason() string {
	app := b.App
	if app == "" {
		app = "unknown"
	}
	return fmt.Sprintf("mai: approve a session for the agent %s. App: %s. Folder: %s. Process: %d.",
		b.Agent, app, b.Folder, b.PID)
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
	l, line, ok := b.route(ctx, id, hasID, line)
	if !ok {
		return nil
	}
	token := l.current()
	answer, err := b.post(ctx, token, line)
	if isLeaseUnknown(err) {
		b.logf("the server does not know the lease of the session %s; the bridge asks for a new lease", l.session())
		if rerr := b.renew(ctx, l, token); rerr != nil {
			return b.renewFailed(id, hasID, l, rerr)
		}
		answer, err = b.post(ctx, l.current(), line)
		if isLeaseUnknown(err) {
			if l.handle == "" {
				b.writeError(id, -32001, "mai: the lease ended. Start the MCP server again.")
				return errStop
			}
			b.drop(l, err)
			if hasID {
				b.writeResult(id, toolError("SESSION_UNKNOWN", msgUnknownHandle))
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
// For the pool lease, the bridge stops. A named session that the server refuses goes, and
// the call gets the code of the server.
func (b *Bridge) renewFailed(id json.RawMessage, hasID bool, l *lease, err error) error {
	if l.handle == "" {
		b.writeError(id, -32001, "mai: the lease ended and no new lease is available: "+err.Error())
		return errStop
	}
	var ae *apiError
	switch {
	case errors.Is(err, errEnded):
		if hasID {
			b.writeResult(id, toolError("SESSION_UNKNOWN", msgUnknownHandle))
		}
	case errors.As(err, &ae):
		b.drop(l, err)
		if hasID {
			code, msg := ae.Code, ae.Message
			if code == "" {
				code, msg = "SESSION_UNKNOWN", msgUnknownHandle
			}
			b.writeResult(id, toolError(code, "The session "+l.session()+" ended: "+msg))
		}
	default:
		if hasID {
			b.writeError(id, -32603, "mai: no new lease for the session "+l.session()+": "+err.Error())
		}
	}
	return nil
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
// ends each lease of the bridge. Cancel ctx on SIGINT or SIGTERM.
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
			fmt.Fprintf(b.Stderr, "mai mcp: the keychain has no key for the agent %s. Run \"mai agent add %s\".\n", b.Agent, b.Agent)
		} else {
			b.logf("no lease for the agent %s: %v", b.Agent, err)
		}
		return ExitError
	}
	b.logf("the agent %s has the session %s", b.Agent, a.Session)

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
			b.writeError(nil, -32001, "mai: the lease ended and no new lease is available: "+err.Error())
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
