package lifecycle

import (
	"regexp"
	"testing"
)

func TestClusterNamesAreUniqueAndValidForKind(t *testing.T) {
	seen := make(map[ClusterName]bool)
	for range 10000 {
		n := NewClusterName()
		if n == "" {
			t.Fatal("generated an empty name, which kind would resolve to the developer's \"kind\" cluster")
		}
		if len(n) > nameMaxLen || !validName.MatchString(string(n)) {
			t.Fatalf("name %q is not a valid kind cluster name", n)
		}
		if seen[n] {
			t.Fatalf("duplicate name %q", n)
		}
		seen[n] = true
	}
}

func TestRunIDCarries48RandomBits(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(runID) {
		t.Fatalf("runID %q is not 12 hex characters, so separate test binaries could mint the same names", runID)
	}
	if other := newRunID(); other == runID {
		t.Fatalf("two run IDs are equal: %s", other)
	}
}
