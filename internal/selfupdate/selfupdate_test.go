package selfupdate

import "testing"

func TestVersionAhead(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"v1.2.0", "v1.1.0", true},   // newer tag
		{"v1.1.0", "v1.2.0", false},  // downgrade is not an update
		{"v1.2.0", "v1.2.0", false},  // same tag
		{"v1.2.0", "1.2.0", false},   // v-prefix is optional
		{"v1.2.0", "dev", true},      // unparseable build: offer the tag
		{"v1.2.0", "", true},         // unset version
		{"garbage", "v1.2.0", false}, // unparseable tag: never update
		// A git-describe build (v1.2.0-4-gdeadbee) is already past the
		// tag — treating it as updateable would downgrade the binary.
		{"v1.2.0", "v1.2.0-4-gdeadbee", false},
		{"v1.3.0", "v1.2.0-4-gdeadbee", true},
	}
	for _, tc := range cases {
		if got := versionAhead(tc.candidate, tc.current); got != tc.want {
			t.Errorf("versionAhead(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}
