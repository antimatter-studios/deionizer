package main

import (
	"testing"
	"time"
)

// between must return the FIRST complete block: the primary (shutdown) reveal on
// STDOUT is the sole ICO_JSON block, and the safety-net reveal lives on STDERR
// under its own sentinels, so first-block extraction stays byte-identical to the
// pre-watchdog behaviour.
func TestBetweenFirstBlock(t *testing.T) {
	const b, e = "\x1eBEGIN\x1e", "\x1eEND\x1e"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"none", "no sentinels here", ""},
		{"one", b + "only" + e, "only"},
		{"first of two", b + "first" + e + "noise" + b + "second" + e, "first"},
		{"begin without end", b + "runaway-no-end", ""},
		{"empty block", b + e, ""},
	}
	for _, c := range cases {
		if got := between(c.in, b, e); got != c.want {
			t.Errorf("%s: between = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseTimeoutSeconds(t *testing.T) {
	ok := []struct {
		in   string
		want time.Duration
	}{
		{"60", 60 * time.Second},
		{"0", 0}, // 0 disables the watchdog
		{"5", 5 * time.Second},
	}
	for _, c := range ok {
		got, err := parseTimeoutSeconds(c.in)
		if err != nil || got != c.want {
			t.Errorf("parseTimeoutSeconds(%q) = %v, %v; want %v, nil", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"-1", "abc", "1.5", ""} {
		if _, err := parseTimeoutSeconds(bad); err == nil {
			t.Errorf("parseTimeoutSeconds(%q) = nil error, want an error", bad)
		}
	}
}

// The --timeout flag must be parsed off the argument list, default to the
// watchdog default when absent, and accept 0 to disable.
func TestParseArgsTimeout(t *testing.T) {
	if _, opt, err := parseArgs([]string{"decode", "x.php"}); err != nil || opt.fileTimeout != defaultFileTimeout {
		t.Errorf("default fileTimeout = %v (err %v), want %v", opt.fileTimeout, err, defaultFileTimeout)
	}
	if _, opt, err := parseArgs([]string{"decode", "x.php", "--timeout", "30"}); err != nil || opt.fileTimeout != 30*time.Second {
		t.Errorf("--timeout 30 fileTimeout = %v (err %v), want 30s", opt.fileTimeout, err)
	}
	if _, opt, err := parseArgs([]string{"decode", "--timeout=0", "x.php"}); err != nil || opt.fileTimeout != 0 {
		t.Errorf("--timeout=0 fileTimeout = %v (err %v), want 0", opt.fileTimeout, err)
	}
	if _, _, err := parseArgs([]string{"decode", "--timeout", "nope"}); err == nil {
		t.Error("--timeout nope: expected a parse error")
	}
	if _, _, err := parseArgs([]string{"decode", "--timeout"}); err == nil {
		t.Error("--timeout with no value: expected an error")
	}
}
