// Package bridge is "mai mcp": it gets a lease for an agent and bridges the MCP stdio of an
// app to POST /mcp of the server.
//
// The lease token is only in the memory of the bridge. It is never in a file, an argument,
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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/monorithm/mai/cli/internal/hook"
	"github.com/monorithm/mai/cli/internal/keychain"
)

// Exit codes of Run.
const (
	ExitOK    = 0
	ExitError = 1
)

// maxLine is the largest JSON-RPC line that the bridge reads from the app.
const maxLine = 64 << 20

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

	mu        sync.Mutex
	lease     string
	session   string
	heartbeat time.Duration
	outMu     sync.Mutex
	renewMu   sync.Mutex
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

// Reason is the text of the presence prompt for a new lease.
func (b *Bridge) Reason() string {
	app := b.App
	if app == "" {
		app = "unknown"
	}
	return fmt.Sprintf("mai: approve a session for the agent %s. App: %s. Folder: %s. Process: %d.",
		b.Agent, app, b.Folder, b.PID)
}

// errNoKey means that the keychain has no key for the agent.
var errNoKey = errors.New("no agent key")

// acquire does the presence check, reads the agent key, and gets a new lease.
func (b *Bridge) acquire(ctx context.Context) error {
	if b.Presence == nil {
		return errors.New("no presence check")
	}
	if err := b.Presence(b.Reason()); err != nil {
		return fmt.Errorf("the presence check failed: %w", err)
	}
	key, err := b.Store.Get(keychain.Service, keychain.AgentAccount(b.Instance, b.Agent))
	if errors.Is(err, keychain.ErrNotFound) {
		return errNoKey
	}
	if err != nil {
		return fmt.Errorf("the keychain read failed: %w", err)
	}
	body, _ := json.Marshal(map[string]any{"app": b.App, "folder": b.Folder, "pid": b.PID})
	var out struct {
		Lease            string `json:"lease"`
		Session          string `json:"session"`
		HeartbeatSeconds int    `json:"heartbeat_seconds"`
	}
	if err := b.callJSON(ctx, http.MethodPost, "/api/leases", key, body, &out); err != nil {
		return err
	}
	if !strings.HasPrefix(out.Lease, "lease_") {
		return errors.New("the server gave no lease")
	}
	hb := time.Duration(out.HeartbeatSeconds) * time.Second
	if hb <= 0 {
		hb = 30 * time.Second
	}
	b.mu.Lock()
	b.lease, b.session, b.heartbeat = out.Lease, out.Session, hb
	b.mu.Unlock()
	b.logf("the agent %s has the session %s", b.Agent, out.Session)
	return nil
}

// renew gets a new lease when the lease old is still the current lease. When a different
// call already got a new lease, renew does nothing.
func (b *Bridge) renew(ctx context.Context, old string) error {
	b.renewMu.Lock()
	defer b.renewMu.Unlock()
	if b.currentLease() != old {
		return nil
	}
	return b.acquire(ctx)
}

func (b *Bridge) currentLease() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lease
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

// beat sends one heartbeat and writes the notice file.
func (b *Bridge) beat(ctx context.Context) error {
	lease := b.currentLease()
	var out struct {
		Session string `json:"session"`
		NewMail int    `json:"new_mail"`
	}
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := b.callJSON(hctx, http.MethodPost, "/api/leases/current/heartbeat", lease, []byte("{}"), &out)
	if isLeaseUnknown(err) {
		b.logf("the server does not know the lease; the bridge asks for a new lease")
		if err := b.renew(ctx, lease); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		// A network error does not stop the bridge. The next heartbeat tries again.
		b.logf("the heartbeat failed: %v", err)
		return nil
	}
	if out.Session != "" {
		b.mu.Lock()
		b.session = out.Session
		b.mu.Unlock()
	}
	b.writeNotice(hook.Notice{Session: out.Session, NewMail: out.NewMail, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
	return nil
}

func (b *Bridge) noticePath() string { return hook.NoticePath(b.StateDir, b.AppPID, b.Agent) }

// writeNotice writes the notice file with mode 0600 in a folder with mode 0700. The file
// holds no secret.
func (b *Bridge) writeNotice(n hook.Notice) {
	if b.StateDir == "" || n.Session == "" {
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

// end deletes the lease and the notice file.
func (b *Bridge) end() {
	if b.StateDir != "" {
		_ = os.Remove(b.noticePath())
	}
	lease := b.currentLease()
	if lease == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.callJSON(ctx, http.MethodDelete, "/api/leases/current", lease, nil, nil); err != nil && !isLeaseUnknown(err) {
		b.logf("the end of the lease failed: %v", err)
	}
	b.mu.Lock()
	b.lease = ""
	b.mu.Unlock()
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

// handle sends one JSON-RPC line to /mcp and writes the answer.
func (b *Bridge) handle(ctx context.Context, line []byte) error {
	id, hasID, err := requestID(line)
	if err != nil {
		b.writeError(nil, -32700, "parse error")
		return nil
	}
	lease := b.currentLease()
	answer, err := b.post(ctx, lease, line)
	if isLeaseUnknown(err) {
		b.logf("the server does not know the lease; the bridge asks for a new lease")
		if rerr := b.renew(ctx, lease); rerr != nil {
			b.writeError(id, -32001, "mai: the lease ended and no new lease is available: "+rerr.Error())
			return errStop
		}
		answer, err = b.post(ctx, b.currentLease(), line)
		if isLeaseUnknown(err) {
			b.writeError(id, -32001, "mai: the lease ended. Start the MCP server again.")
			return errStop
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

// Run gets a lease, bridges stdin to /mcp until stdin ends or ctx ends, and then ends the
// lease. Cancel ctx on SIGINT or SIGTERM.
func (b *Bridge) Run(ctx context.Context) int {
	if err := harden(); err != nil {
		b.logf("the process protection failed: %v", err)
		return ExitError
	}
	if b.Client == nil {
		b.Client = &http.Client{Transport: &http.Transport{Proxy: nil}}
	}
	if err := b.acquire(ctx); err != nil {
		if errors.Is(err, errNoKey) {
			fmt.Fprintf(b.Stderr, "mai mcp: the keychain has no key for the agent %s. Run \"mai agent add %s\".\n", b.Agent, b.Agent)
		} else {
			b.logf("no lease for the agent %s: %v", b.Agent, err)
		}
		return ExitError
	}
	ctx, cancel := context.WithCancel(ctx)
	fatal := make(chan error, 1)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		b.heartbeatLoop(ctx, fatal)
	}()
	// Stop the heartbeat before the end of the lease. Then no heartbeat writes the notice
	// file again after its removal.
	defer func() {
		cancel()
		<-hbDone
		b.end()
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

	for {
		select {
		case <-ctx.Done():
			return ExitOK
		case err := <-fatal:
			b.writeError(nil, -32001, "mai: the lease ended and no new lease is available: "+err.Error())
			b.logf("stop: %v", err)
			return ExitError
		case line, ok := <-lines:
			if !ok {
				return ExitOK
			}
			if err := b.handle(ctx, line); err != nil {
				return ExitError
			}
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

func (b *Bridge) heartbeatLoop(ctx context.Context, fatal chan<- error) {
	for {
		if err := b.beat(ctx); err != nil {
			if ctx.Err() == nil {
				fatal <- err
			}
			return
		}
		b.mu.Lock()
		d := b.heartbeat
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
}
