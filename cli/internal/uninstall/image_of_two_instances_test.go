package uninstall

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The image that mai pulled for mai stays while the container of mai-dev uses it. The
// uninstall of mai-dev, the last instance, must then remove the image, or tell that it stays.
func TestTheUninstallOfTheLastInstanceRemovesTheImageThatStayedForIt(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev") // The container of mai-dev uses the image sha256:img3.
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai"
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall --instance mai: exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "kept     image sha256:img3: a different container uses it") {
		t.Fatalf("the test needs the image to stay for mai-dev:\n%s", out.String())
	}
	out.Reset()
	u = e.uninstaller(&out)
	u.Instance = "mai-dev"
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall --instance mai-dev: exit %d:\n%s", code, out.String())
	}
	if e.eng.findImage("sha256:img3") >= 0 && !strings.Contains(out.String(), "sha256:img3") {
		t.Errorf("after the uninstall of the last instance, the image sha256:img3 that mai pulled stays, "+
			"and the uninstall does not tell it:\n%s", out.String())
	}
}
