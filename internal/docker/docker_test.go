package docker

import (
	"strings"
	"testing"
)

// capBuf keeps the FIRST cap bytes (where the driver flushes its reveal) and
// silently drops the rest, but must always "accept" every write so the pipe
// drain from a runaway container never stalls.
func TestCapBufKeepsHeadNeverStalls(t *testing.T) {
	c := &capBuf{cap: 10}
	n, err := c.Write([]byte("HEAD-of-reveal"))
	if err != nil || n != len("HEAD-of-reveal") {
		t.Fatalf("Write returned n=%d err=%v; want full length, nil", n, err)
	}
	// A second (overflowing) write is still fully accepted, but not retained.
	n, err = c.Write([]byte("xxxxxxxxxxxxxxxxxxxx"))
	if err != nil || n != 20 {
		t.Fatalf("overflow Write returned n=%d err=%v; want 20, nil", n, err)
	}
	if got := c.String(); got != "HEAD-of-re" {
		t.Errorf("capBuf kept %q, want the first 10 bytes %q", got, "HEAD-of-re")
	}
}

// managedName must be unique per call so a force-remove in one run can never
// target another run's container, and greppable by the deionizer- prefix.
func TestManagedNameUniqueAndPrefixed(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		n := managedName()
		if !strings.HasPrefix(n, "deionizer-") {
			t.Fatalf("name %q lacks the deionizer- prefix", n)
		}
		if seen[n] {
			t.Fatalf("duplicate managed name %q", n)
		}
		seen[n] = true
	}
}
