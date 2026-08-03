package main

// Single-input resolution for `decode`: the positional argument may be a
// directory (the original case), one local file, or an http(s) URL whose body
// is fetched into a temp file first — the decoder itself only ever reads local
// bytes, so "by URL" is a transport detail settled here, before the pipeline.

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// maxDownloadBytes caps a URL fetch so a hostile or mis-pasted link cannot fill
// the disk; an encoded PHP file is kilobytes, so 64 MB is generous headroom.
const maxDownloadBytes = 64 << 20

// downloadTimeout bounds the whole fetch; a hung server should not hang the CLI.
const downloadTimeout = 60 * time.Second

// isURL reports whether arg is an absolute http(s) URL — the only schemes we
// will fetch. Anything else is treated as a local path.
func isURL(arg string) bool {
	u, err := url.Parse(arg)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// urlBasename derives a safe local filename from a URL's path: the final path
// element, stripped of any characters outside a conservative set, with a fixed
// fallback when the URL names nothing usable ("/", a trailing slash, ...).
func urlBasename(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "download.php"
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return "download.php"
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if s := b.String(); strings.Trim(s, "._") != "" {
		return s
	}
	return "download.php"
}

// resolveInput turns the decode argument into a local path. A URL is downloaded
// into a fresh temp dir; the returned cleanup removes whatever was staged (for
// plain local paths it is a no-op).
func resolveInput(arg string) (local string, cleanup func(), err error) {
	if !isURL(arg) {
		abs, err := filepath.Abs(arg)
		return abs, func() {}, err
	}
	return download(arg)
}

// download fetches the URL into <tmpdir>/<sanitized-basename> and returns the
// staged path. Any failure after the temp dir exists removes it.
func download(raw string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "deionizer-fetch-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(dir) }

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(raw)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("fetch %s: %w", raw, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cleanup()
		return "", func() {}, fmt.Errorf("fetch %s: %s", raw, resp.Status)
	}

	dest := filepath.Join(dir, urlBasename(raw))
	f, err := os.Create(dest)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	// LimitReader+1 so an oversized body is detected, not silently truncated into
	// something the loader would then mis-decode.
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("fetch %s: %w", raw, err)
	}
	if n > maxDownloadBytes {
		cleanup()
		return "", func() {}, fmt.Errorf("fetch %s: body exceeds %d bytes", raw, maxDownloadBytes)
	}
	return dest, cleanup, nil
}
