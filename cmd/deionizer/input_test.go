package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsURL(t *testing.T) {
	cases := []struct {
		arg  string
		want bool
	}{
		{"https://example.com/lib/encoded.php", true},
		{"http://example.com/encoded.php?dl=1", true},
		{"./src", false},
		{"/abs/path/encoded.php", false},
		{"encoded.php", false},
		{"ftp://example.com/encoded.php", false}, // only http(s) is fetched
		{"https://", false},                      // no host
		{"", false},
	}
	for _, c := range cases {
		if got := isURL(c.arg); got != c.want {
			t.Errorf("isURL(%q) = %v, want %v", c.arg, got, c.want)
		}
	}
}

func TestURLBasename(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://example.com/lib/TextFormat.class.php", "TextFormat.class.php"},
		{"https://example.com/encoded.php?dl=1&x=2", "encoded.php"}, // query dropped
		{"https://example.com/", "download.php"},
		{"https://example.com", "download.php"},
		{"https://example.com/a b/c+d.php", "c_d.php"}, // unsafe chars sanitized
		{"://not a url", "download.php"},
		{"https://example.com/...", "download.php"}, // nothing but dots
	}
	for _, c := range cases {
		if got := urlBasename(c.url); got != c.want {
			t.Errorf("urlBasename(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestResolveInputLocalPath(t *testing.T) {
	local, cleanup, err := resolveInput("some/local/file.php")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !filepath.IsAbs(local) {
		t.Errorf("resolveInput returned non-absolute path %q", local)
	}
	if isURL(local) {
		t.Errorf("local path %q misdetected as URL", local)
	}
}

func TestDownloadStagesBody(t *testing.T) {
	body := "<?php //004fb encoded-payload"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	local, cleanup, err := resolveInput(srv.URL + "/lib/encoded.php?x=1")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if filepath.Base(local) != "encoded.php" {
		t.Errorf("staged basename = %q, want encoded.php", filepath.Base(local))
	}
	got, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("staged body = %q, want %q", got, body)
	}

	// cleanup must remove the staged file.
	cleanup()
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Errorf("staged file still present after cleanup: %v", err)
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	_, cleanup, err := resolveInput(srv.URL + "/missing.php")
	defer cleanup()
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got %v", err)
	}
}

func TestDownloadRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxDownloadBytes+1024))
	}))
	defer srv.Close()

	_, cleanup, err := resolveInput(srv.URL + "/huge.php")
	defer cleanup()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size-cap error, got %v", err)
	}
}

func TestDownloadUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	_, cleanup, err := resolveInput(url + "/x.php")
	defer cleanup()
	if err == nil {
		t.Error("expected a fetch error for an unreachable server")
	}
}

/* -------------------- decodeOne / cmdDecode guard paths -------------------- */

// These cover the pre-docker guards only; anything past them needs a container.

func TestDecodeOneRejectsSkeleton(t *testing.T) {
	err := decodeOne("whatever.php", options{skeleton: true})
	if err == nil || !strings.Contains(err.Error(), "--skeleton") {
		t.Errorf("expected --skeleton rejection, got %v", err)
	}
}

func TestDecodeOneRejectsPlainPHP(t *testing.T) {
	f := filepath.Join(t.TempDir(), "plain.php")
	if err := os.WriteFile(f, []byte("<?php echo 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := decodeOne(f, options{})
	if err == nil || !strings.Contains(err.Error(), "not ionCube-encoded") {
		t.Errorf("expected not-encoded rejection, got %v", err)
	}
}

func TestCmdDecodeNeedsInput(t *testing.T) {
	if err := cmdDecode(nil, options{}); err == nil {
		t.Error("expected an error for missing input")
	}
}

func TestCmdDecodeMissingPath(t *testing.T) {
	err := cmdDecode([]string{filepath.Join(t.TempDir(), "does-not-exist")}, options{})
	if err == nil {
		t.Error("expected a stat error for a missing path")
	}
}
