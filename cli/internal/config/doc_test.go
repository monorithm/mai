package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// referencePath is the reference of the configuration, from the folder of this package.
var referencePath = filepath.Join("..", "..", "..", "docs", "reference", "configuration.md")

// The section "Configuration file of the CLI" of the reference gives each field of
// config.json, in its table and in its example, and no other field.
func TestTheReferenceGivesEachFieldOfTheConfigurationFile(t *testing.T) {
	b, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	start := strings.Index(text, "## Configuration file of the CLI")
	if start < 0 {
		t.Fatal("the reference has no section \"Configuration file of the CLI\"")
	}
	section := text[start+3:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	var want []string
	ct := reflect.TypeFor[Config]()
	for i := range ct.NumField() {
		want = append(want, strings.Split(ct.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(want)
	for what, re := range map[string]*regexp.Regexp{
		"table":   regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|"),
		"example": regexp.MustCompile(`(?m)^\s+"([a-z_]+)":`),
	} {
		var got []string
		for _, m := range re.FindAllStringSubmatch(section, -1) {
			got = append(got, m[1])
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("the %s of the reference gives the fields %v; config.json has the fields %v", what, got, want)
		}
	}
}
