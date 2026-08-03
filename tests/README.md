# tests/ — decoder correctness harness

An automated **RED-GREEN** harness that measures, repeatably, how close the
`deionizer` decoder is to 100% correct — and exactly which PHP constructs
fail.

## Concept

We control the source, so it is true ground truth:

```
ground-truth fixture  --(official ionCube trial encoder)-->  encoded PHP
        |                                                          |
        |                                                    (deionizer decode)
        v                                                          v
   run original  <-------- diff (3 tiers) -------->        run decoded artifact
```

For every fixture × PHP version × obfuscation level the harness records three
tiers, from weakest to strongest evidence:

| Tier | Meaning |
|---|---|
| **artifact** | the decoder emitted a `*.decoded-source.php` at all |
| **lint** | that artifact passes `php -l` (is valid PHP) |
| **behavior** | the artifact, run through a driver, reproduces the original's runtime output **byte-for-byte** |

**Behavior is the headline metric.** Every PASS is a real encode → decode → run
diff — nothing is asserted from inspection.

## Run it

```sh
./tests/run.sh                              # full matrix
VERSIONS="7.4" OBF="none" ./tests/run.sh    # quick single-cell smoke
FIXTURES="for_loop closures" ./tests/run.sh # subset of constructs
```

Outputs:
- `tests/results/results.json` — machine-readable matrix (every cell + summary).
- `tests/results/dashboard.md` — the dashboard (pass/fail matrix + success rate).

### Requirements
- `docker` (Desktop with linux/amd64 emulation — the trial encoder is an x86-64
  Linux binary; the PHP 5.6 runtime is amd64-only).
- `go` (1.21+, stdlib only — the harness adds no module dependencies).
- The decoder binary at `/tmp/deionizer` (override with `DECODER=...`).
- The ionCube Encoder 15 trial extracted under
  `examples/ioncube-trial/ioncube_encoder_evaluation/bin/` (override `ENCODER_DIR`).

### Env overrides
`DECODER`, `ENCODER_DIR`, `ENCODER_IMAGE`, `RUNTIMES`, `VERSIONS`, `METHODS`, `OBF`,
`FIXTURES`, `WORK_DIR`, `RESULTS`, `DASHBOARD`.

`METHODS` selects the encoding-method axis (space-separated). It takes precedence
over the legacy `OBF`, which is translated (`none`->`plain`, `all`->`obfuscate-all`)
so `OBF=none VERSIONS=7.4` remains the canonical single-cell smoke.

## Encoding methods (the widened axis)

Each method is one matrix column; every cell is a real encode -> decode -> run -> diff.

| Method | Encoder flags | Notes |
|---|---|---|
| `plain` | *(none)* | baseline |
| `optimise-none` | *(none)* | encoder rejects an explicit `--optimise none`; omitting the flag IS "none", so this is identical to `plain` |
| `optimise-more` | `--optimise more` | bytecode optimisation |
| `optimise-max` | `--optimise max` | maximum bytecode optimisation |
| `obfuscate-all` | `--obfuscate all --obfuscation-key deionizer-harness` | one-way name obfuscation |
| `dynamic-keys` | *(driven by `@ioncube.dk` source annotations, not a flag)* | observation on its own fixture |

Behavior is graded **name-independently**: the decoded artifact is run as a whole
program and its stdout is diffed against the original's — never by calling a
hardcoded entry symbol (which obfuscation renames). See `driver/run_probe.php`.

## Layout

```
tests/
  run.sh                     one-command entrypoint (builds + runs the harness)
  harness/                   the runner (stdlib-only Go, package main)
    main.go                  encode -> decode -> compare orchestration + results
    dashboard.go             Markdown matrix + success-rate renderer
  driver/
    run_probe.php            name-independent whole-program runner (no hardcoded symbol)
    compare.php              batch lint + behavior comparator (runs in the php image)
  fixtures/
    *.php                    one construct per file: defines probe() + a top-level `echo probe();` shim
    dynkeys/probe-dynkeys.php  Dynamic-Keys observation fixture (@ioncube.dk RANDOM)
  results/
    results.json             latest machine-readable results
    work/                    per-cell encode/decode scratch (gitignored)
```

## Fixtures

Each fixture exercises **one** construct and declares its applicability in a
header the runner parses:

```php
<?php
// construct: nested foreach
// minphp: 5.6
// maxphp: 8.4
function probe() { /* ... */ return "deterministic string"; }

// Whole-program behavioral shim: running this file prints the deterministic
// result, so a decoded artifact is graded by behavior, not by the entry symbol.
echo probe();
```

`probe()` must return a deterministic string, and the top-level `echo probe();`
shim makes the fixture a self-emitting whole program. The runner computes the
expected output by running the **original** under the target PHP version (true
ground truth), then compares the decoded artifact's output to it — grading by
what the decode *computes*, not by whether a symbol named `probe` survived
(obfuscation renames it one-way; see `driver/run_probe.php`). Fixtures outside a
version's range (e.g. arrow functions on 5.6, `match` before 8.0) are marked
`n/a`, not FAIL.

## Versions

Runnable versions are read from `runtimes.yml`: any version with a non-empty
`decode_image` is exercised; one without (currently **8.1**) is reported as
**pending** and activates automatically once its decode image lands.

## Dynamic Keys

`fixtures/dynkeys/probe-dynkeys.php` is encoded with source `@ioncube.dk … RANDOM`
annotations (not a CLI flag) and decoded. The harness only **observes and records**
what the decoder recovers from dynamic-key-protected methods — no evasion is built.
