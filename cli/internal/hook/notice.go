// Package hook gives the output of "mai hook": the count of new mail of each session of the
// bridge of the app, in the hook format of the app. The output has one line for each
// session with new mail.
//
// The bridge writes a notice file. The hook finds the file through the process chain and
// never calls the server.
package hook

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Notice is the content of a notice file. It holds no secret and no mail text.
//
// Session and NewMail are the pool session of the bridge. Named has one notice for each
// named session that a chat logged in to through the bridge, in login order. A notice in
// Named has no Named list.
type Notice struct {
	Session   string   `json:"session"`
	NewMail   int      `json:"new_mail"`
	UpdatedAt string   `json:"updated_at"`
	Named     []Notice `json:"named,omitempty"`
}

// All gives the pool session first, then each named session.
func (n Notice) All() []Notice {
	out := make([]Notice, 0, 1+len(n.Named))
	out = append(out, Notice{Session: n.Session, NewMail: n.NewMail, UpdatedAt: n.UpdatedAt})
	for _, s := range n.Named {
		if s.Session != "" {
			out = append(out, Notice{Session: s.Session, NewMail: s.NewMail, UpdatedAt: s.UpdatedAt})
		}
	}
	return out
}

// NoticeDir is the folder of the notice files in the state folder.
func NoticeDir(stateDir string) string { return filepath.Join(stateDir, "notices") }

// NoticePath is the notice file of the bridge of one app process and one agent.
func NoticePath(stateDir string, appPID int, agent string) string {
	return filepath.Join(NoticeDir(stateDir), strconv.Itoa(appPID)+"-"+agent+".json")
}

// FindNotice returns the notice of the nearest process of pids that has a notice file. With
// an agent, only the file of that agent counts. With no agent, the first file of the process
// in name order counts.
func FindNotice(stateDir, agent string, pids []int) (Notice, bool) {
	dir := NoticeDir(stateDir)
	for _, pid := range pids {
		if agent != "" {
			if n, ok := readNotice(NoticePath(stateDir, pid, agent)); ok {
				return n, true
			}
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, strconv.Itoa(pid)+"-*.json"))
		for _, m := range matches {
			if !strings.HasPrefix(filepath.Base(m), strconv.Itoa(pid)+"-") {
				continue
			}
			if n, ok := readNotice(m); ok {
				return n, true
			}
		}
	}
	return Notice{}, false
}

// maxNoticeSize stops the read of a file that is too large to be a notice. A notice with
// many named sessions is still much smaller.
const maxNoticeSize = 64 << 10

func readNotice(path string) (Notice, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Notice{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxNoticeSize+1))
	if err != nil || len(data) == 0 || len(data) > maxNoticeSize {
		return Notice{}, false
	}
	var out Notice
	if json.Unmarshal(data, &out) != nil || out.Session == "" {
		return Notice{}, false
	}
	return out, true
}
