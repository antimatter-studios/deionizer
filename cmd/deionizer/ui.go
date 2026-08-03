package main

// Progress reporting for the CLI.
//
// A decode is one `docker run` PER ENCODED FILE, each a second or more, so a
// tree of a few hundred files is minutes of silence unless something says what
// it is doing. This is that something.
//
// Two streams, deliberately:
//
//	stdout  WHAT happened — headings, one line per file, the summary
//	stderr  HOW it happened — every docker command, container stderr, timings
//
// so `deionizer decode tree/ > record.txt` keeps a readable record while
// --verbose noise still goes to the terminal. The trace stream is silent unless
// --verbose asked for it.

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"deionizer/internal/docker"
)

// nameWidth caps the file-name column so one deeply nested path cannot push the
// status column off the right of an 80-column terminal.
const nameWidth = 46

type ui struct {
	out, err io.Writer
	verbose  bool
	// items reports per-file progress. `decode` wants it; `process` prints a
	// one-row-per-project table instead and would have that table shredded by
	// hundreds of file lines, so it turns items off unless --verbose.
	items bool
}

// newUI builds the reporter and, when verbose, wires the docker package's trace
// hook to it — that is what turns "silent subprocess" into "here is the exact
// command I ran".
func newUI(verbose bool) *ui {
	u := &ui{out: os.Stdout, err: os.Stderr, verbose: verbose, items: true}
	if verbose {
		docker.Trace = u.tracef
	}
	return u
}

// head prints a top-level step: the ">> " lines that bracket a run.
func (u *ui) head(format string, args ...any) {
	fmt.Fprintf(u.out, ">> "+format+"\n", args...)
}

// info prints a detail line under the current step.
func (u *ui) info(format string, args ...any) {
	fmt.Fprintf(u.out, "   "+format+"\n", args...)
}

// tracef prints only under --verbose. Container commands, cache hits, sizes.
func (u *ui) tracef(format string, args ...any) {
	if !u.verbose {
		return
	}
	fmt.Fprintf(u.err, "        · "+format+"\n", args...)
}

// traceBlock echoes captured child output (a container's stderr, say) under
// --verbose, one prefixed line at a time so it cannot be mistaken for ours.
func (u *ui) traceBlock(label, text string) {
	if !u.verbose {
		return
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(u.err, "        | %s: %s\n", label, line)
	}
}

// warn always prints, to the trace stream: something went wrong but the run
// continues.
func (u *ui) warn(format string, args ...any) {
	fmt.Fprintf(u.err, "   "+format+"\n", args...)
}

/* ------------------------------- per-item -------------------------------- */

// item is one file's progress line. Non-verbose it is a single line completed
// in place ("path.php ....... ok"); verbose it opens a heading and the trace
// lines for that file appear beneath it, because a docker command echoed
// mid-line would tear the line in half.
type item struct {
	u      *ui
	name   string
	start  time.Time
	open   bool // the line is started and awaiting its verdict
	silent bool // progress suppressed; only a failure is worth a word
}

func (u *ui) item(idx, total int, name string) *item {
	it := &item{u: u, name: name, start: time.Now()}
	if !u.items {
		it.silent = true
		return it
	}
	shown := truncTail(name, nameWidth)
	w := len(fmt.Sprint(total))
	if u.verbose {
		fmt.Fprintf(u.out, "   [%*d/%d] %s\n", w, idx, total, shown)
		return it
	}
	fmt.Fprintf(u.out, "   [%*d/%d] %-*s ", w, idx, total, nameWidth, shown)
	it.open = true
	return it
}

func (it *item) ok(format string, args ...any)   { it.done("ok  ", format, args...) }
func (it *item) skip(format string, args ...any) { it.done("--  ", format, args...) }
func (it *item) fail(format string, args ...any) { it.done("ERR ", format, args...) }

func (it *item) done(status, format string, args ...any) {
	detail := fmt.Sprintf(format, args...)
	if it.silent {
		// A suppressed run still reports what broke: only a failure is worth a word.
		if strings.HasPrefix(status, "ERR") {
			it.u.warn("[decode] %s: %s", it.name, detail)
		}
		return
	}
	took := dur(time.Since(it.start))
	if it.open {
		fmt.Fprintf(it.u.out, "%s %s (%s)\n", status, detail, took)
		it.open = false
		return
	}
	// Verbose: the heading is already on its own line, so indent the verdict to
	// sit under the trace lines it belongs to.
	fmt.Fprintf(it.u.out, "        %s %s (%s)\n", status, detail, took)
}

/* -------------------------------- helpers -------------------------------- */

// dur formats a duration the way a progress line wants it: no microseconds, no
// "1.0000000002s".
func dur(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// plural is the difference between "1 files" and looking finished.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// truncTail keeps s within n columns by replacing its head with a leading ellipsis,
// so the tail — the part that tells deeply nested paths apart — stays visible.
// Returns s unchanged when it already fits.
func truncTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n+1:]
}
