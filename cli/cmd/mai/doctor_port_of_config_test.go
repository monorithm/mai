package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/config"
)

// The port of config.json is the port of the server of mai-wf-b, and the container of
// mai-wf-a, the instance of config.json, runs on a different port. "mai doctor" must report
// a problem: the commands of mai-wf-a send its keys to the port of config.json.
func TestDoctorReportsAPortOfConfigThatIsNotThePortOfTheContainer(t *testing.T) {
	_, portB, _ := configOfA(t)
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(`{"instance":"`+instA+`","port":`+portB+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := captureLifecycle(t)
	_ = runDoctor(nil)
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "ok      port") {
			t.Errorf("mai doctor gives %q, but the container of %s has the port %d, and the server of %s uses the port %s:\n%s",
				line, instA, portOfA, instB, portB, out.String())
		}
	}
	if !strings.Contains(out.String(), "problem port") || !strings.Contains(out.String(), "step: run: mai start.") {
		t.Errorf("mai doctor gives no problem with the step mai start for the port of config.json:\n%s", out.String())
	}
}
