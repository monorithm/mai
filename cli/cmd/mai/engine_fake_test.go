package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/engine"
)

// fakeCtr is a container of the fake Engine API.
type fakeCtr struct {
	ID, Name string
	Image    string // The image ID.
	Instance string // The label of the instance; "" for a container with no label.
	Port     int    // The host port of the HTTP port; 0 for no binding.
	Data     string // The host folder of /data; "" for a volume.
	Running  bool
	Labels   map[string]string // More labels, for example the labels of a 2.x install.
}

// engineFake is the part of the Engine API that the lifecycle commands use, in memory. It
// records each call, and each body of a request to make a container. A request to make a
// container gets an error, thus a command stops after that request.
type engineFake struct {
	mu      sync.Mutex
	ctrs    []fakeCtr
	images  map[string]string // reference -> image ID
	calls   []string          // method and path, with no API version
	created []map[string]any
}

var apiVersionPrefix = regexp.MustCompile(`^/v[0-9.]+`)

// called tells if the fake got a request with the method and the path.
func (f *engineFake) called(method, path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == method+" "+path {
			return true
		}
	}
	return false
}

func (f *engineFake) find(idOrName string) (fakeCtr, bool) {
	for _, c := range f.ctrs {
		if c.ID == idOrName || c.Name == idOrName {
			return c, true
		}
	}
	return fakeCtr{}, false
}

func (c fakeCtr) labels() map[string]string {
	l := map[string]string{}
	if c.Instance != "" {
		l = engine.Labels(c.Instance)
	}
	for k, v := range c.Labels {
		l[k] = v
	}
	return l
}

func (c fakeCtr) mounts() []map[string]any {
	if c.Data == "" {
		return []map[string]any{{"Type": "volume", "Name": c.Instance + "3-data", "Destination": "/data"}}
	}
	return []map[string]any{{"Type": "bind", "Source": c.Data, "Destination": "/data"}}
}

func (c fakeCtr) inspect() map[string]any {
	status := "exited"
	if c.Running {
		status = "running"
	}
	host := map[string]any{}
	if c.Port != 0 {
		host["PortBindings"] = map[string]any{"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(c.Port)}}}
	}
	return map[string]any{
		"Id": c.ID, "Name": "/" + c.Name, "Image": c.Image,
		"State":      map[string]any{"Status": status, "Running": c.Running},
		"Config":     map[string]any{"Image": "mai:test", "Labels": c.labels()},
		"HostConfig": host,
		"Mounts":     c.mounts(),
	}
}

func (f *engineFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := apiVersionPrefix.ReplaceAllString(r.URL.Path, "")
	f.calls = append(f.calls, r.Method+" "+p)
	w.Header().Set("Content-Type", "application/json")
	fail := func() {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"test: not in the fake"}`)
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case p == "/_ping":
		w.Header().Set("API-Version", "1.47")
		_, _ = io.WriteString(w, "OK")
	case p == "/version":
		_, _ = io.WriteString(w, `{"Version":"29.0.0","ApiVersion":"1.47"}`)
	case r.Method == http.MethodGet && p == "/containers/json":
		var out []map[string]any
		for _, c := range f.ctrs {
			// Docker Desktop gives the path of its VM for a bind mount in a list, for example
			// /host_mnt/private/tmp/x. Only the inspect gives the host path.
			mounts := c.mounts()
			if c.Data != "" {
				mounts = []map[string]any{{"Type": "bind", "Source": "/host_mnt" + c.Data, "Destination": "/data"}}
			}
			out = append(out, map[string]any{"Id": c.ID, "Names": []string{"/" + c.Name}, "ImageID": c.Image,
				"Labels": c.labels(), "Mounts": mounts})
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "containers" && parts[2] == "json":
		c, ok := f.find(parts[1])
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"No such container: `+parts[1]+`"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(c.inspect())
	case r.Method == http.MethodGet && p == "/images/json":
		var out []map[string]any
		for ref, id := range f.images {
			out = append(out, map[string]any{"Id": id, "RepoTags": []string{ref}})
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && p == "/volumes":
		_, _ = io.WriteString(w, `{"Volumes":[]}`)
	case r.Method == http.MethodPost && p == "/volumes/create":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"Name": body["Name"]})
	case r.Method == http.MethodPost && p == "/containers/create":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		fail()
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "containers" && parts[2] == "stop":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "containers":
		w.WriteHeader(http.StatusNoContent)
	default:
		fail()
	}
}

// useEngineFake makes the lifecycle commands use the fake Engine API f.
func useEngineFake(t *testing.T, f *engineFake) {
	t.Helper()
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	api, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(s.URL, "http://")), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	e := &engine.Engine{API: api}
	old := lcConnectEngine
	lcConnectEngine = func(context.Context, io.Writer) (*engine.Engine, error) { return e, nil }
	t.Cleanup(func() { lcConnectEngine = old })
}

// createdPort gives the host port of the HTTP port in the body of a request to make a
// container.
func createdPort(body map[string]any) string {
	host, _ := body["HostConfig"].(map[string]any)
	pb, _ := host["PortBindings"].(map[string]any)
	list, _ := pb["8080/tcp"].([]any)
	if len(list) == 0 {
		return ""
	}
	b, _ := list[0].(map[string]any)
	s, _ := b["HostPort"].(string)
	return s
}

// createdData gives the source of the mount at /data in the body of a request to make a
// container: a host folder or a volume.
func createdData(body map[string]any) string {
	host, _ := body["HostConfig"].(map[string]any)
	mounts, _ := host["Mounts"].([]any)
	for _, m := range mounts {
		mm, _ := m.(map[string]any)
		if mm["Target"] == "/data" {
			s, _ := mm["Source"].(string)
			return s
		}
	}
	return ""
}
