// Command deionizer is the single host-side binary for the tool: PHP-version
// auto-detection, the runtime matrix, docker build/run orchestration, interface
// recovery, and deep decode.
//
//	deionizer process <tree>            auto-detect + FULL-decode each project (default)
//	deionizer --skeleton process <tree> fast interface-only skeletons instead
//	deionizer decode <dir>              deep-decode a tree in place (<file>.decoded-source.php)
//	deionizer decode <file|URL>         decode one file (URL fetched first) to stdout
//	deionizer decode <tree> --output D  mirror-decode into D + a confidence report
//	deionizer build [X.Y]               build/ensure a runtime image
//	deionizer shell <dir> [X.Y]         debug shell in the matched container
//
// Full decode is the DEFAULT; --skeleton selects the fast interface-only path.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"deionizer/internal/docker"
)

// defaultFileTimeout bounds a single file's decode container. It is the watchdog
// that makes a pathological file (an encoded bootstrap whose warmed body never
// returns) a clean per-file error instead of a run that hangs and leaks a
// container. Override per invocation with --timeout <seconds> (0 disables it).
//
// It is deliberately generous. The guard is against an UNBOUNDED (effectively
// infinite) warm, so a large bound still turns hours into minutes; the cost of a
// too-TIGHT bound is worse — a legitimately slow file under emulation (PHP 5.6
// runs x86-64-emulated and, under heavy host load, a normally-2s decode can
// balloon) would be false-killed and fall back to the pre-warm reveal, which is
// missing exactly the methods a warm would have materialised. That yields a
// wrong/empty decode, so headroom over the legitimate worst case matters more
// than reacting fast to the rare true hang. Point a known-pathological tree at a
// small --timeout to reap its runaways sooner.
const defaultFileTimeout = 120 * time.Second

// version and buildDate are stamped at release time via -ldflags -X (see
// .goreleaser.yml: -X main.version / -X main.buildDate). A plain `go build`
// leaves them at these defaults, so a local build reports "dev".
var (
	version   = "dev"
	buildDate = ""
)

// options holds everything the binary needs to run, and it is populated ONLY from
// the command line — never from the process environment. Ambient config (os.Getenv
// reach-outs) is deliberately avoided so the path from user configuration to
// behaviour is explicit and traceable: a caller (e.g. the `chore` runner reading
// a .env file) passes each value as a flag, and it flows from here by parameter.
type options struct {
	skeleton    bool          // interface-only mode
	php         string        // forced PHP runtime version ("" = auto-detect)
	verbose     bool          // trace every docker command, container stderr and timing
	fileTimeout time.Duration // per-file decode watchdog (0 = unbounded)

	// output, when set, switches decode into MIRROR mode: every encoded file under
	// the input is decoded into this directory (the source tree stays untouched)
	// and a confidence report is written there. Empty = decode in place.
	output string

	// Deployment config — supplied on the command line, defaulted to neutral local
	// paths where a default makes sense, empty where the user must provide it.
	extDir     string // deep-decode extension build context (default: tmp/deionizer-ext)
	loaderBaseURL string // ionCube loader download base (required for image builds)
	guardConsts   string // extra app bootstrap guard constants, comma-separated
	runtimesPath  string // overlay runtime matrix file ("" = built-in only)
}

func main() {
	// Ctrl-C (or SIGTERM) cancels this context; docker.UseContext threads it into
	// every `docker run` so an interrupt actually stops the current container and
	// the decode loop, instead of being ignored.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	docker.UseContext(ctx)

	args, opt, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "deionizer:", err)
		os.Exit(2)
	}
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "process":
		err = cmdProcess(rest, opt)
	case "decode":
		err = cmdDecode(rest, opt)
	case "build":
		err = cmdBuild(rest, opt)
	case "shell":
		err = cmdShell(rest, opt)
	case "version", "--version":
		// Bare version on stdout, one line, so a release self-check can
		// `grep -qx` it and the Homebrew formula test can assert_match it.
		fmt.Println(version)
		if buildDate != "" {
			fmt.Fprintln(os.Stderr, "built "+buildDate)
		}
		return
	case "help", "-h", "--help":
		usage(os.Stdout)
		return
	default:
		fmt.Fprintf(os.Stderr, "deionizer: unknown command %q\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deionizer:", err)
		os.Exit(1)
	}
}

// parseArgs pulls the global flags (--skeleton, --php X.Y / --php=X.Y) out of the
// argument list wherever they appear, leaving the ordered positional arguments.
func parseArgs(in []string) (positional []string, opt options, err error) {
	opt.fileTimeout = defaultFileTimeout
	// strVal reads the value for a string flag given as either "--flag value" or
	// "--flag=value". It advances i past a consumed separate value.
	strVal := func(i *int, name string) (string, bool, error) {
		a := in[*i]
		if a == name {
			if *i+1 >= len(in) {
				return "", false, fmt.Errorf("%s requires a value", name)
			}
			*i++
			return in[*i], true, nil
		}
		if p := name + "="; strings.HasPrefix(a, p) {
			return a[len(p):], true, nil
		}
		return "", false, nil
	}
	// The string-valued flags all read the same way (--flag value / --flag=value), so
	// a {name, dest} table routes each through strVal instead of a copy-pasted block.
	strFlags := []struct {
		name string
		dest *string
	}{
		{"--php", &opt.php},
		{"--ext-dir", &opt.extDir},
		{"--loader-base-url", &opt.loaderBaseURL},
		{"--guard-consts", &opt.guardConsts},
		{"--runtimes", &opt.runtimesPath},
		{"--output", &opt.output},
	}
	for i := 0; i < len(in); i++ {
		a := in[i]

		matched := false
		for _, f := range strFlags {
			v, ok, e := strVal(&i, f.name)
			if e != nil {
				return nil, opt, e
			}
			if ok {
				*f.dest = v
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		// --timeout takes its value the same two ways, so it routes through strVal
		// too; only its seconds->Duration conversion is special.
		if v, ok, e := strVal(&i, "--timeout"); e != nil {
			return nil, opt, e
		} else if ok {
			if opt.fileTimeout, err = parseTimeoutSeconds(v); err != nil {
				return nil, opt, err
			}
			continue
		}

		switch {
		case a == "--skeleton":
			opt.skeleton = true
		case a == "--verbose" || a == "-v":
			opt.verbose = true
		default:
			positional = append(positional, a)
		}
	}
	return positional, opt, nil
}

// parseTimeoutSeconds reads a per-file timeout given in seconds; 0 disables the
// watchdog (an unbounded run, restoring the pre-watchdog behaviour on request).
func parseTimeoutSeconds(s string) (time.Duration, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid --timeout %q: want a whole number of seconds (0 to disable)", s)
	}
	return time.Duration(n) * time.Second, nil
}

func usage(w *os.File) {
	fmt.Fprint(w, `deionizer — recover ionCube-encoded PHP through a version-matched loader.

USAGE
  deionizer process <tree>              auto-detect each project's PHP version and
                                             FULL-decode it (default): reveal -> oplines ->
                                             decompile -> <file>.decoded-source.php
  deionizer --skeleton process <tree>   fast interface-only mode: exact class/method
                                             signatures via the loader (no bodies)
  deionizer decode <dir> [--php X.Y]    force deep decode of one tree, writing
                                             <file>.decoded-source.php in place
  deionizer decode <file|URL>           decode ONE file — a local path or an
                                             http(s) URL fetched first — and write
                                             the recovered source to stdout (progress
                                             goes to stderr, so the source pipes
                                             clean: ... decode x.php > x.recovered.php)
  deionizer decode <tree> --output DIR  MIRROR mode: deep-decode every encoded file
                                             into DIR (the source tree is untouched)
                                             and write a per-file CONFIDENCE REPORT
                                             there (confidence-report.json + .txt):
                                             detected PHP, method count, php -l
                                             verdict, and an N ok / partial / failed
                                             rollup
  deionizer build [X.Y]                 build/ensure the runtime image (default 7.4)
  deionizer shell <dir> [X.Y]           interactive shell in the matched container
  deionizer version                     print the binary version and exit

FLAGS
  --skeleton        interface-only recovery instead of full decode
  --output DIR      decode into a MIRROR of the input under DIR instead of writing
                    in place, and write a confidence report there (decode only)
  --php X.Y         force the PHP runtime instead of auto-detecting
  --timeout N       per-file decode watchdog in seconds (default 120; 0 disables).
                    A file whose container exceeds it is force-removed and marked
                    a clean per-file error, so no single file wedges or leaks a run.
                    Lower it for a known-pathological tree to reap runaways sooner
  --verbose, -v     trace every docker command, container stderr and per-file
                    timing on stderr (progress itself is always on stdout)

CONFIGURATION (deployment-specific; passed on the command line, never read from
the environment — a runner such as `+"`chore`"+` reads your .env and passes these):
  --loader-base-url URL   base URL the ionCube loader bundles are fetched from at
                          image-build time. Required to build an image; the tool
                          does not hardcode the vendor's distribution endpoint
  --ext-dir DIR        deep-decode extension build context (default: tmp/deionizer-ext).
                          Not needed for --skeleton
  --guard-consts LIST     extra app bootstrap guard constants to define before a
                          module loads, comma-separated NAME or NAME=VALUE
  --runtimes FILE         overlay the built-in runtime matrix with this YAML file

Version auto-detection reads the ionCube marker; version-less markers are resolved
by trial-loading one file and reading the loader's "Encoder for PHP X.Y" verdict.
`)
}
