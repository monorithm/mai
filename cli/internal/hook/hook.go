package hook

import (
	"io"
)

// maxPayload is the part of the payload that the hook reads. The hook reads the remainder
// and discards it, so that the app does not get a broken pipe.
const maxPayload = 1 << 20

// Run reads the payload on in, finds the notice of the app in stateDir through pids (the
// process chain, nearest first), and writes the output to out. Run never fails: on an
// error it writes nothing.
func Run(in io.Reader, out io.Writer, format, event, agent, stateDir string, pids []int) {
	defer func() { _ = recover() }()
	var raw []byte
	if in != nil {
		raw, _ = io.ReadAll(io.LimitReader(in, maxPayload))
		_, _ = io.Copy(io.Discard, in)
	}
	n, ok := FindNotice(stateDir, agent, pids)
	if !ok {
		return
	}
	text := Render(format, event, ParsePayload(raw), n)
	if text == "" {
		return
	}
	_, _ = io.WriteString(out, text+"\n")
}
