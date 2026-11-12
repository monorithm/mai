package version

import "testing"

func TestFromModule(t *testing.T) {
	cases := map[string]string{
		"v3.0.0":                               "3.0.0",
		"v3.1.12":                              "3.1.12",
		"(devel)":                              "",
		"":                                     "",
		"v3.0.1-0.20261112181224-ee8c679a1b2c": "",
		"v3.0.0+dirty":                         "",
		"v3.0.0-rc.1":                          "",
	}
	for in, want := range cases {
		if got := fromModule(in); got != want {
			t.Errorf("fromModule(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestATestBuildKeepsTheDevelopmentVersion(t *testing.T) {
	if Version != Dev {
		t.Errorf("Version = %q, want %q", Version, Dev)
	}
}
