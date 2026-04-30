package boxlandcmd

import "testing"

// TestVersionConstant guards the version string from accidental empty values.
// Real version stamping comes later via build flags.
func TestVersionConstant(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
