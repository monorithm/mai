package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI has no presence check, no approval log, and no fake of the presence check. Thus
// the README and the guides in docs tell about none of them. The design document keeps the
// history of each decision, thus the test does not read the folder docs/design.
func TestTheGuidesTellAboutNoPresenceCheck(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	files := []string{filepath.Join(root, "README.md")}
	docs := filepath.Join(root, "docs")
	err := filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path == filepath.Join(docs, "design") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("the test found only %d files of the docs", len(files))
	}
	terms := []string{"Touch ID", "Windows Hello", "polkit", "mai approvals", "approvals.json",
		"presencefake", "MAI_TEST_KEYCHAIN_SERVICE", "each-bridge", "app-instance"}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, term := range terms {
				if strings.Contains(line, term) {
					t.Errorf("%s:%d tells about %q: %s", f, i+1, term, strings.TrimSpace(line))
				}
			}
		}
	}
}
