package version

import "testing"

func TestCurrentNonEmpty(t *testing.T) {
	if got := Current(); got == "" {
		t.Fatalf("Current() returned empty string")
	}
}

func TestCurrentMatchesEmbeddedVersion(t *testing.T) {
	// Sanity: the embed pulled the file in, not just whitespace.
	got := Current()
	if got == "0.0.0-dev" {
		t.Fatalf("Current() == %q — embedded VERSION file looks empty", got)
	}
	if _, err := Parse(got); err != nil {
		t.Fatalf("Current() = %q is not parseable SemVer: %v", got, err)
	}
}

func TestOverrideWins(t *testing.T) {
	prev := override
	t.Cleanup(func() { override = prev })
	override = "v9.9.9"
	if got := Current(); got != "9.9.9" {
		t.Fatalf("Current() with override = %q, want 9.9.9", got)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in            string
		major, minor  int
		patch         int
		pre, build    string
		expectErr     bool
	}{
		{"0.1.0", 0, 1, 0, "", "", false},
		{"v1.2.3", 1, 2, 3, "", "", false},
		{"V10.20.30", 10, 20, 30, "", "", false},
		{"1.2.3-rc1", 1, 2, 3, "rc1", "", false},
		{"1.2.3+abc", 1, 2, 3, "", "abc", false},
		{"1.2.3-rc.2+build.5", 1, 2, 3, "rc.2", "build.5", false},
		{"not-a-version", 0, 0, 0, "", "", true},
		{"1.2", 0, 0, 0, "", "", true},
		{"1.2.x", 0, 0, 0, "", "", true},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if (err != nil) != c.expectErr {
			t.Errorf("Parse(%q) err=%v expectErr=%v", c.in, err, c.expectErr)
			continue
		}
		if c.expectErr {
			continue
		}
		if got.Major != c.major || got.Minor != c.minor || got.Patch != c.patch {
			t.Errorf("Parse(%q) = %d.%d.%d, want %d.%d.%d",
				c.in, got.Major, got.Minor, got.Patch, c.major, c.minor, c.patch)
		}
		if got.PreRelease != c.pre || got.Build != c.build {
			t.Errorf("Parse(%q) pre=%q build=%q, want pre=%q build=%q",
				c.in, got.PreRelease, got.Build, c.pre, c.build)
		}
	}
}

func TestCompareOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.1.1", -1},
		{"0.1.1", "0.1.0", +1},
		{"0.2.0", "0.1.99", +1},
		{"1.0.0", "0.99.99", +1},
		{"v1.0.0", "1.0.0", 0},                        // leading v stripped
		{"1.0.0-rc1", "1.0.0", -1},                    // pre-release < release
		{"1.0.0", "1.0.0-rc1", +1},
		{"1.0.0-alpha", "1.0.0-beta", -1},             // lexical pre-release
		{"1.0.0+build.1", "1.0.0+build.2", 0},         // build metadata ignored
		{"garbage", "0.1.0", -1},                      // unparseable < parseable
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	if !IsNewer("0.1.0", "0.2.0") {
		t.Errorf("IsNewer(0.1.0, 0.2.0) should be true")
	}
	if IsNewer("0.2.0", "0.1.0") {
		t.Errorf("IsNewer(0.2.0, 0.1.0) should be false")
	}
	if IsNewer("0.1.0", "0.1.0") {
		t.Errorf("IsNewer(0.1.0, 0.1.0) should be false")
	}
}
