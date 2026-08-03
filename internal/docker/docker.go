// Package docker centralises every interaction with the `docker` CLI for
// deionizer: image existence checks, `docker run` with mounts / platform /
// network options, and building the runtime images from Dockerfile templates that
// are EMBEDDED in this binary (go:embed), so there is no loose Dockerfile to
// maintain.
//
// It shells out to `docker` (exec.Command) rather than using the SDK: the daemon
// socket, buildkit and platform emulation are already configured for the CLI, and
// the surface we need is small.
package docker

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"text/template"
	"time"
)

// runCtx is cancelled on Ctrl-C (SIGINT) once the CLI calls UseContext, so an
// in-flight `docker run` is stopped (see Run) and decode loops can bail out
// between files instead of ignoring the interrupt.
var runCtx = context.Background()

// UseContext installs the process-wide cancellation context — set once from main
// after signal.NotifyContext. A nil ctx is ignored so this stays optional.
func UseContext(ctx context.Context) {
	if ctx != nil {
		runCtx = ctx
	}
}

// Interrupted reports whether runCtx has been cancelled (Ctrl-C), so a caller can
// stop a loop between iterations rather than launch the next container.
func Interrupted() bool { return runCtx.Err() != nil }

//go:embed embed/Dockerfile.oracle.tmpl
var dockerfileOracle string

//go:embed embed/Dockerfile.decode.tmpl
var dockerfileDecode string

//go:embed embed/Dockerfile.decode56.tmpl
var dockerfileDecode56 string

//go:embed embed/analyze.php
var analyzePHP string

// Trace, when set, receives every docker command this package runs, and the
// notes about why (image cache hits, build contexts). It is nil by default —
// the CLI wires it to --verbose — so nothing here prints unless asked.
var Trace func(format string, args ...any)

func tracef(format string, args ...any) {
	if Trace != nil {
		Trace(format, args...)
	}
}

// Mount is one bind mount into the container.
type Mount struct {
	Host      string // absolute host path
	Container string // path inside the container
	RO        bool   // read-only
}

func (m Mount) arg() string {
	s := m.Host + ":" + m.Container
	if m.RO {
		s += ":ro"
	}
	return s
}

// RunOpts tunes a `docker run`.
type RunOpts struct {
	Platform    string // e.g. "linux/amd64"; "" for native
	Network     string // e.g. "none"; "" leaves the default
	Entrypoint  string // override the image ENTRYPOINT
	Interactive bool   // allocate a TTY and wire stdio (debug shell)
	Env         []string
	Timeout     time.Duration // >0 bounds a single run (watchdog); 0 = no per-run bound
}

// ErrTimeout is returned by Run when a per-run Timeout elapsed before the
// container exited. The container has been force-removed by the time it is
// returned, so a caller can classify a timed-out file as a clean per-file error
// and move on. Errors.Is-friendly.
var ErrTimeout = errors.New("docker run timed out")

// maxCapturedOutput bounds how much stdout/stderr Run retains from one container.
// A pathological encoded file whose warm loop runs away can emit output without
// bound (megabytes of trace before the watchdog fires); keeping the FIRST slice
// preserves the reveal the driver flushes up front and discards the runaway tail,
// so a timed-out decode still yields whatever was recovered without risking OOM.
const maxCapturedOutput = 128 << 20

// killGrace is how long Run waits after signalling the docker client (SIGINT via
// cmd.Cancel) before force-killing it, so an interrupted client can finish tearing
// its container down instead of being SIGKILLed mid-cleanup.
const killGrace = 5 * time.Second

// containerSeq makes each managed container name unique within this process so a
// force-remove in one run can never target another run's container.
var containerSeq atomic.Uint64

// managedName returns a unique, greppable container name. `docker ps` filtering
// on this prefix is how a run proves it left nothing behind.
func managedName() string {
	return fmt.Sprintf("deionizer-%d-%d", os.Getpid(), containerSeq.Add(1))
}

// forceRemove tears a managed container down unconditionally: `docker rm -f`
// stops and removes it in one step. This is the orphan-killer — signalling the
// `docker run` CLIENT does NOT reliably stop the container it launched, so every
// managed run defers this to guarantee nothing survives a timeout, a Ctrl-C, or
// any other early exit. A missing container (already gone via --rm) is not an
// error worth surfacing.
func forceRemove(name string) {
	if name == "" {
		return
	}
	_ = exec.Command("docker", "rm", "-f", name).Run()
}

// capBuf is an io.Writer that keeps at most cap bytes but keeps accepting (and
// discarding) writes past that, so the child never blocks on a full pipe.
type capBuf struct {
	b   bytes.Buffer
	cap int
}

func (c *capBuf) Write(p []byte) (int, error) {
	if room := c.cap - c.b.Len(); room > 0 {
		if len(p) <= room {
			c.b.Write(p)
		} else {
			c.b.Write(p[:room])
		}
	}
	return len(p), nil // always "accept" so io.Copy from the pipe never stalls
}

func (c *capBuf) String() string { return c.b.String() }

// ImageName is the interface-recovery image naming convention: deionizer:php<ver>.
func ImageName(ver string) string { return "deionizer:php" + ver }

// ImageExists reports whether a local image is present.
func ImageExists(name string) bool {
	if name == "" {
		return false
	}
	ok := exec.Command("docker", "image", "inspect", name).Run() == nil
	tracef("docker image inspect %s -> %s", name, presence(ok))
	return ok
}

func presence(ok bool) string {
	if ok {
		return "present"
	}
	return "absent"
}

// Run executes `docker run --rm ...` and captures stdout/stderr. For an
// interactive run (opts.Interactive) stdio is inherited and the returned strings
// are empty.
func Run(image string, mounts []Mount, args []string, opts RunOpts) (stdout, stderr string, err error) {
	cargs := []string{"run", "--rm"}
	if opts.Interactive {
		cargs = append(cargs, "-it")
	}
	if opts.Network != "" {
		cargs = append(cargs, "--network", opts.Network)
	}
	if opts.Platform != "" {
		cargs = append(cargs, "--platform", opts.Platform)
	}
	if opts.Entrypoint != "" {
		cargs = append(cargs, "--entrypoint", opts.Entrypoint)
	}
	for _, e := range opts.Env {
		cargs = append(cargs, "-e", e)
	}
	for _, m := range mounts {
		cargs = append(cargs, "-v", m.arg())
	}
	cargs = append(cargs, image)
	cargs = append(cargs, args...)
	tracef("docker %s", strings.Join(cargs, " "))

	if opts.Interactive {
		// Interactive shell keeps the terminal + Ctrl-C wired straight through.
		cmd := exec.Command("docker", cargs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return "", "", cmd.Run()
	}
	return runManaged(cargs, opts)
}

// runManaged executes one non-interactive `docker run` under the watchdog: it
// names the container so it can be force-removed, bounds it with a per-run
// Timeout on top of the process-wide Ctrl-C context, and — crucially —
// GUARANTEES the container is torn down on every exit path (normal, timeout,
// Ctrl-C, force-kill of the client). cargs already carries the run flags but not
// yet the container name or the image/command tail; managedName is spliced in
// right after `run`.
//
// Why the explicit teardown and not just --rm + signalling the client: a
// `docker run` client that is signalled or killed does NOT reliably stop the
// container it started, which is exactly how a hung file used to orphan a
// container for hours. So on ANY return we `docker rm -f` the name (deferred),
// and the per-run context cancels the client first so the buffered output is
// captured before the kill.
func runManaged(cargs []string, opts RunOpts) (stdout, stderr string, err error) {
	name := managedName()
	// Splice `--name <name>` immediately after the leading `run` verb.
	named := make([]string, 0, len(cargs)+2)
	named = append(named, cargs[0], "--name", name)
	named = append(named, cargs[1:]...)

	// Per-run watchdog: derive a context from the process-wide one so BOTH a
	// Ctrl-C (runCtx) and the per-file Timeout can stop this run.
	ctx := runCtx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(runCtx, opts.Timeout)
		defer cancel()
	}

	// The guarantee: whatever happens below, the container does not survive this
	// function. cmd.Run has already returned by the time this defer fires, so the
	// client is done being waited on; `docker rm -f` then reaps the container the
	// signalled/killed client may have left running.
	defer forceRemove(name)

	cmd := exec.CommandContext(ctx, "docker", named...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = killGrace
	out := &capBuf{cap: maxCapturedOutput}
	errb := &capBuf{cap: maxCapturedOutput}
	cmd.Stdout, cmd.Stderr = out, errb
	runErr := cmd.Run()

	// Classify a watchdog stop: DeadlineExceeded is our per-file timeout (a clean
	// per-file error), Canceled is Ctrl-C (surfaced as-is so loops stop). The
	// captured output is still returned — the driver flushes its reveal up front,
	// so a timed-out file can still decode from what was captured before the kill.
	if opts.Timeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out.String(), errb.String(), fmt.Errorf("%w after %s", ErrTimeout, opts.Timeout)
	}
	return out.String(), errb.String(), runErr
}

// EnsureOracleImage builds the interface-recovery image for ver if it is missing.
// loaderBaseURL is the vendor's loader download base; it is a required input for a
// build (the loader is fetched from the vendor at build time and this repo does not
// hardcode the URL). It is passed in by the caller — never read from the
// environment here — so configuration stays an explicit argument.
func EnsureOracleImage(ver, platform, loaderBaseURL string) error {
	img := ImageName(ver)
	if ImageExists(img) {
		return nil
	}
	return BuildOracleImage(ver, platform, loaderBaseURL)
}

// BuildOracleImage generates the oracle Dockerfile from the embedded template and
// runs `docker build` in a temp context that carries the embedded analyze.php.
func BuildOracleImage(ver, platform, loaderBaseURL string) error {
	if loaderBaseURL == "" {
		return fmt.Errorf("no loader download base configured: the ionCube loader is fetched " +
			"from the vendor at image-build time and this tool does not hardcode their distribution " +
			"URL. Pass --loader-base-url https://<vendor-download-host>/loader_downloads")
	}
	ctx, err := os.MkdirTemp("", "deionizer-oracle-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(ctx)

	df, err := render(dockerfileOracle, map[string]string{"PHPVersion": ver})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ctx, "Dockerfile"), []byte(df), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(ctx, "src"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ctx, "src", "analyze.php"), []byte(analyzePHP), 0o644); err != nil {
		return err
	}
	return build(ctx, "Dockerfile", ImageName(ver), platform, map[string]string{
		"PHP_VERSION":     ver,
		"LOADER_BASE_URL": strings.TrimSuffix(loaderBaseURL, "/"),
	})
}

// EnsureDecodeImage builds the deep-decode image for ver if it is missing.
// srcContext must point at the decode-extension source tree (decode.c /
// decode56.c + config.m4). If the image is absent and no source tree is available
// it returns a descriptive error rather than silently failing.
func EnsureDecodeImage(ver, image, platform, srcContext string) error {
	if image == "" {
		return fmt.Errorf("no decode_image configured for PHP %s (deep decode not ported for this version)", ver)
	}
	if ImageExists(image) {
		return nil
	}
	if srcContext == "" || !dirExists(srcContext) {
		return fmt.Errorf("deep decode needs the decode-extension source, which is not shipped with this tool. "+
			"Supply it and set %s to its directory (or drop it at tmp/deionizer-ext), then retry. "+
			"Interface recovery (--skeleton) needs none of this. (image %q)", "DEIONIZER_EXT_DIR", image)
	}
	tmpl := dockerfileDecode
	if strings.HasPrefix(ver, "5.") {
		tmpl = dockerfileDecode56
	}
	df, err := render(tmpl, map[string]string{"PHPVersion": ver})
	if err != nil {
		return err
	}
	dfPath := filepath.Join(srcContext, ".deionizer.Dockerfile.gen")
	if err := os.WriteFile(dfPath, []byte(df), 0o644); err != nil {
		return err
	}
	defer os.Remove(dfPath)
	return buildFile(srcContext, dfPath, image, platform, nil)
}

func build(context, dockerfile, tag, platform string, buildArgs map[string]string) error {
	return buildFile(context, filepath.Join(context, dockerfile), tag, platform, buildArgs)
}

func buildFile(context, dockerfilePath, tag, platform string, buildArgs map[string]string) error {
	args := []string{"build"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	for k, v := range buildArgs {
		args = append(args, "--build-arg", k+"="+v)
	}
	args = append(args, "-f", dockerfilePath, "-t", tag, context)
	tracef("docker %s", strings.Join(args, " "))

	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr // stream build logs to our stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build %s: %w", tag, err)
	}
	return nil
}

func render(tmpl string, data any) (string, error) {
	t, err := template.New("dockerfile").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
