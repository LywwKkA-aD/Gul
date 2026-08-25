package update

import "testing"

func TestParseRejectsWhatIsNotAVersion(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		"", "   ", "1", "1.2", "1.2.3.4", "v", "va.b.c",
		"1.2.x", "1.2.3-", "1.2.3-alpha..1", "1.2.3+",
		"01.2.3", "1.02.3", "1.2.3-alpha.01",
		"1.2.3-alpha_1", "release-notes", "<html>404</html>",
	} {
		if v, ok := Parse(s); ok {
			t.Errorf("Parse(%q) = %+v, true; want not ok", s, v)
		}
	}
}

func TestParseAcceptsTheFormsGitHubProduces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want Version
	}{
		{"0.4.0", Version{Major: 0, Minor: 4, Patch: 0}},
		{"v0.4.0", Version{Major: 0, Minor: 4, Patch: 0}},
		{" v1.2.3 ", Version{Major: 1, Minor: 2, Patch: 3}},
		{"0.4.0-alpha.1", Version{Minor: 4, Prerelease: []string{"alpha", "1"}}},
		{"v10.20.30-rc.1", Version{Major: 10, Minor: 20, Patch: 30, Prerelease: []string{"rc", "1"}}},
		// Build metadata parses and is then dropped: semver says it takes no
		// part in precedence.
		{"1.0.0+build.7", Version{Major: 1}},
		{"1.0.0-alpha+build-7", Version{Major: 1, Prerelease: []string{"alpha"}}},
	}
	for _, tc := range tests {
		got, ok := Parse(tc.in)
		if !ok {
			t.Fatalf("Parse(%q) = not ok", tc.in)
		}
		if got.Major != tc.want.Major || got.Minor != tc.want.Minor || got.Patch != tc.want.Patch {
			t.Errorf("Parse(%q) core = %d.%d.%d, want %d.%d.%d", tc.in,
				got.Major, got.Minor, got.Patch, tc.want.Major, tc.want.Minor, tc.want.Patch)
		}
		if len(got.Prerelease) != len(tc.want.Prerelease) {
			t.Fatalf("Parse(%q) prerelease = %v, want %v", tc.in, got.Prerelease, tc.want.Prerelease)
		}
		for i := range got.Prerelease {
			if got.Prerelease[i] != tc.want.Prerelease[i] {
				t.Errorf("Parse(%q) prerelease = %v, want %v", tc.in, got.Prerelease, tc.want.Prerelease)
			}
		}
	}
}

// The table the whole feature rests on. The prerelease rows are the ones that
// matter here: every Gul release so far is an alpha, so ordinary semver
// comparison of the numeric parts alone would call 0.4.0-alpha.1 and
// 0.4.0-alpha.2 the same version.
func TestCompare(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want int
	}{
		// Equal, in every spelling.
		{"0.4.0", "0.4.0", 0},
		{"v0.4.0", "0.4.0", 0},
		{"0.4.0-alpha.1", "0.4.0-alpha.1", 0},
		{"1.0.0+build.1", "1.0.0+build.2", 0},

		// Numeric components.
		{"0.3.0", "0.4.0", -1},
		{"0.4.0", "0.3.0", 1},
		{"0.4.0", "1.0.0", -1},
		{"1.2.3", "1.2.10", -1},
		{"0.9.0", "0.10.0", -1},

		// A prerelease precedes the release it leads to.
		{"0.4.0-alpha.1", "0.4.0", -1},
		{"0.4.0", "0.4.0-alpha.1", 1},

		// Prerelease ordering: numeric identifiers compare numerically.
		{"0.4.0-alpha.1", "0.4.0-alpha.2", -1},
		{"0.4.0-alpha.2", "0.4.0-alpha.10", -1},
		{"0.4.0-alpha.10", "0.4.0-alpha.2", 1},

		// Alphanumeric identifiers compare by ASCII, and a numeric one
		// precedes an alphanumeric one.
		{"0.4.0-alpha.1", "0.4.0-beta.1", -1},
		{"0.4.0-beta", "0.4.0-rc", -1},
		{"0.4.0-alpha.1", "0.4.0-alpha.beta", -1},

		// More identifiers win when everything before them is equal.
		{"0.4.0-alpha", "0.4.0-alpha.1", -1},

		// The real sequence this project ships.
		{"0.3.0-alpha.2", "0.4.0-alpha.1", -1},
		{"0.4.0-alpha.1", "0.4.0-alpha.2", -1},
		{"0.4.0-alpha.2", "0.4.0", -1},
	}
	for _, tc := range tests {
		a, ok := Parse(tc.a)
		if !ok {
			t.Fatalf("Parse(%q) failed", tc.a)
		}
		b, ok := Parse(tc.b)
		if !ok {
			t.Fatalf("Parse(%q) failed", tc.b)
		}
		if got := Compare(a, b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := Compare(b, a); got != -tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		candidate, current string
		want               bool
	}{
		{"v0.4.0-alpha.1", "0.3.0-alpha.2", true},
		{"v0.3.0-alpha.2", "0.3.0-alpha.2", false},
		{"v0.3.0-alpha.1", "0.3.0-alpha.2", false},
		{"v0.4.0", "0.4.0-alpha.9", true},
		// A tag we cannot read never announces anything.
		{"latest", "0.3.0-alpha.2", false},
		{"", "0.3.0-alpha.2", false},
		{"v0.4.0", "not-a-version", false},
	}
	for _, tc := range tests {
		if got := IsNewer(tc.candidate, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestShouldAnnounce(t *testing.T) {
	t.Parallel()
	const current = "0.3.0-alpha.2"
	tests := []struct {
		name                       string
		latest, current, dismissed string
		want                       bool
	}{
		{"a newer release with nothing dismissed speaks", "v0.4.0-alpha.1", current, "", true},
		{"the running version says nothing", "v0.3.0-alpha.2", current, "", false},
		{"an older release says nothing", "v0.3.0-alpha.1", current, "", false},
		{"a dismissed version stays quiet", "v0.4.0-alpha.1", current, "v0.4.0-alpha.1", false},
		{"a version newer than the dismissed one speaks again", "v0.4.0-alpha.2", current, "v0.4.0-alpha.1", true},
		{"a release older than the dismissal stays quiet", "v0.4.0-alpha.1", current, "v0.4.0", false},
		{"a dismissal we cannot read silences nothing", "v0.4.0-alpha.1", current, "garbage", true},
		{"whitespace is not a dismissal", "v0.4.0-alpha.1", current, "   ", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldAnnounce(tc.latest, tc.current, tc.dismissed); got != tc.want {
				t.Errorf("ShouldAnnounce(%q, %q, %q) = %v, want %v",
					tc.latest, tc.current, tc.dismissed, got, tc.want)
			}
		})
	}
}
