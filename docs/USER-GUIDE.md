# deionizer — User Guide

Recover readable, runnable PHP from ionCube-encoded files **that you own** but can
no longer read, and get a per-file **confidence report** telling you how far each
file was recovered and whether the result can be trusted.

This guide covers the `decode` flow — decoding a tree in place, a single file to
stdout, or a whole tree into a mirror with a per-file confidence report.

> **In one line:** deionizer does **not** decrypt or crack ionCube. It runs each
> encoded file through a *real, version-matched ionCube loader* inside a throwaway
> container, captures the bytecode the loader itself restores, and decompiles that
> back to PHP. Any code you own, you can run — and running it is enough to recover
> it.
>
> The corollary is the tool's scope boundary: recovery rides the loader's own
> restoration path, so it reaches exactly the code you can already execute, and no
> further. deionizer deliberately does not implement a loader-free or universal
> ionCube decryptor, and extracts no vendor key.

---

## 1. Requirements & install

You need:

- **Docker** — running and able to pull `php:<ver>-cli` base images and to run
  the linux/amd64 platform under emulation for PHP < 7.2. Check with `docker version`.
- The **deionizer binary**. Build it from source — the compiled binary is not
  committed:

```bash
# from the repository root
go build -o bin/deionizer ./cmd/deionizer      # go 1.25+
bin/deionizer help
```

### Configuration — flags, from a `.env` you control

Deployment config is passed to the binary **on the command line**. The binary
never reads the environment: this keeps the path from your config to the tool
explicit and traceable. The `chore` runner makes this ergonomic — copy
`.env.example` to `.env`, fill it in, and `chore` loads it and passes each value
as the matching flag (`.env` → `chore` → `--flags` → binary). Running the binary
directly, you pass the same flags yourself.

- **`--loader-dir DIR`** (`.env`: `DEIONIZER_LOADER_DIR`) — your own unzipped
  ionCube loader directory. This repo deliberately does **not** hardcode, bundle, or
  download the vendor's loader: you get the loaders from ionCube yourself, unzip
  them, and point this at the directory. When an image is built, the tool copies the
  matched `ioncube_loader_lin_<ver>.so` (for the build's architecture) out of it.
  The directory may be arch-split (`x86-64/`, `aarch64/` subdirs — handy when you
  need both, since PHP < 7.2 builds run `linux/amd64`-emulated) or the flat
  `ioncube/…` layout the vendor's zip unpacks to. Required to build any image:

```bash
bin/deionizer build 7.4 --loader-dir /path/to/unzipped/ioncube-loaders
```

- **`--guard-consts LIST`** (`.env`: `DEIONIZER_GUARD_CONSTS`) *(optional)* — a
  comma-separated list of bootstrap guard constants to define before an encoded
  module is loaded. Many host-app modules top out with
  `if (!defined('SOME_GUARD')) die();` so they only run inside their app; recovery
  needs that guard defined or the file reveals nothing. A generic set (`ROOTDIR`,
  `ABSPATH`, `APP_ROOT`, …) is always defined; name any app-specific guard here so
  the tool need not hardcode it (`NAME` or `NAME=VALUE`):

```bash
bin/deionizer decode ./enc --guard-consts 'MYAPP_KERNEL,APP_ENV=production'
```

- **`--ext-dir DIR`** (`.env`: `DEIONIZER_EXT_DIR`) *(needed only for deep
  decode)* — the tool ships two recovery depths:

  - **Interface recovery** (`--skeleton`) — exact class/method/property signatures
    via the loader's own reflection. Works out of the box; needs nothing here.
  - **Deep decode** (the default) — recovers method **bodies** by lifting the
    revealed opcodes. This needs a small PHP **decode extension** whose source is
    **not shipped with the tool**. Supply your own and point `--ext-dir` at its
    directory (default: `tmp/deionizer-ext`). Without it, deep-decode commands stop
    with a clear message and you can still run `--skeleton` recovery.

- **`--runtimes FILE`** (`.env`: `DEIONIZER_RUNTIMES`) *(optional)* — overlay the
  built-in runtime matrix with your own YAML (image tags / platforms per version).

The runtime images (a version-matched loader + the reveal shim, one per PHP
version) are built **on first use** and cached by Docker. The first run for a
given PHP version therefore spends a few minutes building an image; later runs
reuse it. You can pre-build one explicitly:

```bash
bin/deionizer build 7.4        # build/ensure the PHP 7.4 runtime image
```

Which PHP versions are supported for deep decode is defined in
`internal/runtime/runtimes.yml` (currently 5.6, 7.2, 7.4, 8.1, 8.3). Encoded
files targeting an in-between version are mapped to the nearest supported runtime.

---

## 2. Decoding a tree

Point `decode` at a directory of encoded PHP. By default it writes each recovered
file **in place**, next to the encoded original as `<file>.decoded-source.php`:

```bash
bin/deionizer decode <input-dir>
```

To keep your source tree untouched, add `--output <dir>`: `decode` then writes
**every** recovered file into a **mirror** of that directory — your source tree is
only read, never written — and drops a per-file **confidence report** beside them.

```bash
bin/deionizer decode <input-dir> --output <output-dir>
```

- `<input-dir>` — a tree containing ionCube-encoded `.php` files (nested dirs are
  walked; already-decoded `*.decoded-source.php` artifacts are skipped).
- `--output <output-dir>` — optional. Without it, artifacts land in place next to
  each encoded file; with it, recovered files mirror the input's directory layout
  under `<output-dir>` (original filenames kept) and the source tree is never
  written.

Example (a production class tree, mirrored so the source stays read-only):

```bash
bin/deionizer decode \
  /path/to/app/system/class \
  --output ./app-recovered
```

You do **not** pass a PHP version: deionizer auto-detects each file's target from
its ionCube marker, and for version-less markers (`//004fb`, …) it trial-loads one
file and reads the loader's own "Encoder for PHP X.Y" verdict. A whole tree of
identically-marked files is probed only once.

What lands in `<output-dir>` (mirror mode):

```
<output-dir>/
  <mirrored recovered .php files…>     # recovered source, same names/layout as input
  confidence-report.json               # machine-readable report (see §3)
  confidence-report.txt                # the human summary, saved verbatim
```

A single file or an http(s) URL decodes to **stdout** instead, so the recovered
source pipes clean (progress goes to stderr):

```bash
bin/deionizer decode ./one.class.php > one-recovered.php
bin/deionizer decode https://host/enc/one.php > one-recovered.php
```

Add `--verbose` (or `-v`) to trace every `docker` command, the container's own
stderr, and per-file timing on stderr while the readable summary stays on stdout.

### The write modes at a glance

`decode --output` is the reviewer-facing flow (mirror + confidence report). The
same command covers the other write paths:

| Command | Writes | Reports |
|---|---|---|
| `decode <in> --output <out>` | recovered files into a **mirror** + report files | per-file confidence (version, methods, `php -l`, ok/partial/failed) |
| `decode <dir>` | `<file>.decoded-source.php` **in place** | count written / empty / failed |
| `decode <file\|URL>` | recovered source to **stdout** | one-line method count on stderr |
| `process <tree>` | `<file>.decoded-source.php` **in place** | one row per project (files written) |

Use plain `decode`/`process` when you want the artifacts beside the originals; use
`decode --output` when you want an untouched source tree plus a trustworthiness
score.

---

## 3. How to read the confidence report

The terminal (and `confidence-report.txt`) summary looks like this:

```
>> confidence report: /…/app/system/class
   FILE                               PHP   METHODS  LINT    STATUS
   ApiClient.class.php                7.4         3  pass    ok
   ConfigStorage.class.php            7.4        10  pass    ok
   Constants.class.php                7.4         0  skipped failed
   ReportBuilder.class.php            7.4        44  pass    ok
   …
   ------------------------------------------------------------
   12 files: 11 ok, 0 partial, 1 failed

   notes:
   · Constants.class.php                no methods revealed
```

### Per-file fields

| Field (`.txt` / `.json`) | Meaning |
|---|---|
| **FILE** / `file` | Path relative to the input tree. |
| **PHP** / `php_version` | The runtime the loader matched this file to. |
| — / `detected_via` | *How* the version was decided: `from the ionCube marker`, `from a trial-load probe`, or `forced with --php`. |
| **METHODS** / `methods` | Count of functions + methods whose bodies were revealed and decompiled. |
| **LINT** / `lint` | `php -l` verdict on the recovered file, run under a version-matched PHP: `pass`, `fail`, or `skipped`. |
| **STATUS** / `status` | `ok`, `partial`, or `failed` (below). |
| — / `output` | The recovered file, relative to the output tree (present when a file was written). |
| — / `reason` | A short explanation for anything not clean. |

### Status meaning — the confidence stance

The status is deliberately **conservative**: a file is only `ok` when it both
revealed methods **and** the recovered PHP parses cleanly.

| STATUS | Means | Typical `reason` |
|---|---|---|
| `ok` | Methods revealed **and** `php -l` passed. High confidence the file is recovered. | — |
| `partial` | Recovered PHP was written, but it was **not** verified clean — `php -l` failed, or linting couldn't run. Usable, but review it. | `recovered but php -l failed: …` |
| `failed` | Nothing usable was produced. | `no methods revealed`, `reveal: …`, `deep decode not ported for PHP …` |

The **rollup** line (`N files: X ok, Y partial, Z failed`, and `rollup` in the
JSON) is the one number to quote when reporting recovery coverage for a tree.

### What a green `ok` does and does not claim

`php -l` proves the recovered source is **syntactically valid** PHP for that
version. It is a strong, cheap signal — but it is *not* a proof of behavioural
equivalence to the original. The stronger guarantee — recovered code compiled,
executed, and its output compared against the encoded original — is a separate
step, and it is what the correctness harness in [`tests/`](../tests/) measures.
Read `ok` as "revealed and parses", not "byte-for-byte proven".

---

## 4. Known limitations

These follow directly from how recovery works. deionizer does
not paper over them — it reports them as `partial`/`failed` with a reason.

- **Docker and a matching loader are required.** Deep decode runs inside a
  container carrying a real ionCube loader for the file's PHP version. A version
  with no decode runtime in `runtimes.yml` reports `failed: deep decode not ported
  for PHP …`.
- **Only self-running code is recoverable.** Recovery uses the loader's *own*
  restoration path while the file loads. A file that cannot load standalone in the
  sandbox (missing includes/extensions it needs at load time) may reveal nothing
  and show `failed`. This is intrinsic: any code you can run you can recover — and
  only that code.
- **Files with no methods reveal nothing to decompile.** A pure constant/property
  class or an interface of declarations legitimately yields `0` methods and is
  reported `failed: no methods revealed`. That is a true negative about *this*
  file, not a decode error — inspect the original to confirm there was nothing to
  recover. (`convert.class.php` in the example above is such a file.)
- **Name obfuscation is not reversed.** If identifiers were scrambled at encode
  time, bodies still come back (obfuscation does not stop the code from running),
  but the original class/method/variable *names* are not recoverable by inversion
  — only by dictionary/context matching. The
  production target above is **not** name-obfuscated.
- **`php -l` is a syntax gate, not a behavioural proof.** See §3 — `ok` means
  revealed + parses. Comments and original local-variable names are not part of
  bytecode and are not recovered.
- **PHP 5.x scalars are best-effort.** Some 5.x literal constants are encrypted at
  rest and are reconstructed from a behavioural side-channel; where a value is
  inferred rather than read directly it is marked `/* inferred */` in the output.
- **Scope policy.** Point `decode` only at code you own and are entitled to
  recover. deionizer deliberately does **not** implement a loader-free / universal
  ionCube decryptor and does not extract any vendor master key; that line is drawn
  deliberately and stays drawn.

---

## 5. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `failed: reveal: no reveal output …` | The decode image lacks the reveal shim for this version, or the file didn't load. Rerun with `--verbose` to see the container's stderr. |
| `failed: deep decode not ported for PHP X.Y` | No `decode_image` for that runtime in `runtimes.yml`. |
| `partial: recovered but php -l failed: …` | The decompiled PHP has a syntax issue for that version — the `reason` carries the parser's message; open the file at that line. |
| First run is slow / lots of `docker build` output | Expected: the runtime image for that PHP version is being built once and cached. Pre-build with `bin/deionizer build X.Y`. |
| A whole tree reports `failed` | Docker not running, or the wrong tree (no ionCube-encoded `.php` under it). |

---

*See also:* [`README.md`](../README.md) for what recovery does and does not return,
and [`tests/README.md`](../tests/README.md) for how decoder correctness is measured.
