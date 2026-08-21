# AGENTS.md

Operating manual for this repository: how it is built, how it is tested, how a
release reaches `brew install`, and which rules are not negotiable. Written for an
agent picking the project up cold, but it is the same procedure a human follows.

Read [README.md](README.md) first for what the tool *does*; this file is about
working *on* it.

---

## 1. What the pipeline actually is

deionizer is a **host Go CLI that orchestrates Docker**. Nothing container-side and
nothing vendor-supplied ships in this repository. One decode runs four stages:

```
encoded .php ──▶ detect          internal/detect     read the ionCube marker, pick a PHP version
             ──▶ reveal          internal/docker     run the file under a version-matched loader
                                                     in a throwaway container; the decode
                                                     extension dumps op_arrays as JSON
             ──▶ parse/normalise internal/rawparse   JSON ──▶ oplines
                                 internal/opline
             ──▶ render          internal/decompile   oplines ──▶ readable PHP
```

The consequence that shapes every test decision below: **stages 1–2 need vendor
assets** (the ionCube loader `.so`, and the decode extension source), while
**stages 3–4 are pure Go over a JSON capture** and need nothing but the toolchain.
That is why the offline test suite is built on frozen JSON captures — see §5.

| Path | What lives there |
|---|---|
| [cmd/deionizer/](cmd/deionizer/) | CLI: flags, commands, mirror/report writer, terminal UI |
| [internal/](internal/) | the four stages above; `internal/decompile/render.go` is the bulk of the logic |
| [tests/](tests/) | the encode→decode→run correctness harness (Go), fixtures, drivers |
| [chores.yml](chores.yml) | every task; the only supported entry point |
| [runtimes.yml](runtimes.yml) | PHP version → image tag + platform matrix (**duplicated**, see §6) |
| [.githooks/](.githooks/) | github-guard commit/push guards, auto-activated |
| [.goreleaser.yml](.goreleaser.yml) | release build — targets, flags, reproducibility |

---

## 2. Prerequisites

- **Go 1.25+** (`go.mod` pins `go 1.25.0`; the release pins `GOTOOLCHAIN=go1.25.0`
  exactly, because go1.25.0 and go1.25.6 emit different bytes).
- **Docker**, able to pull `php:<ver>-cli` and to emulate `linux/amd64` for PHP < 7.2.
  Only needed for anything that actually decodes.
- **[chore](https://github.com/antimatter-studios/chore)** — the task runner.
  `brew install antimatter-studios/tap/chore`.
- **`.env`** — copy `.env.example`. Config reaches the binary as flags only
  (`.env` → chore → `--flags` → binary); the binary never reads the environment.

```
DEIONIZER_LOADER_DIR    your own unzipped ionCube loader dir   (any image build)
DEIONIZER_EXT_DIR       your own decode-extension source       (deep decode only)
DEIONIZER_GUARD_CONSTS  bootstrap guard constants to define    (some modules)
DEIONIZER_RUNTIMES      overlay runtime matrix                 (optional)
DEIONIZER_CORPUS_DIR    local corpus for coverage tests        (tests only)
```

`chore doctor` checks all of it and tells you what is missing.

---

## 3. Build

```sh
chore build              # → bin/deionizer   (go build -o bin/deionizer ./cmd/deionizer)
chore build:nodecompiler # → bin/deionizer-nodec, decompiler seam off (signatures only)
chore clean
```

`bin/` is gitignored — binaries are artifacts, never committed. The version string
is `dev` in a local build; the real value is stamped by ldflags at release (§7).

The **first `chore` command in a fresh clone activates the git hooks** via a
`lifecycle: before_all` task, so the guards travel with the repo and cannot be
skipped by running a task directly.

---

## 4. Everyday checks

```sh
chore test              # go test -count=1 ./...
chore test:race         # go test -race -count=1 ./...   (what CI runs)
chore test:php          # lint + behavior tiers over the frozen captures (needs php)
chore lint              # gofmt -l check + go vet ./...
chore fmt               # gofmt -w .
chore check:runtimes    # runtimes.yml == internal/runtime/runtimes.yml (data)
chore doctor            # is the toolchain ready
```

`chore lint` must be clean before pushing. A gofmt failure is fatal in CI's very
first step, which means `Vet`, `Test` and `Build` never run — the whole pipeline
goes dark behind a whitespace error.

---

## 5. Testing — two tiers, and why

### Tier 1 — offline, runs in CI, gates every PR

`go test ./...`. The important part is `internal/decompile`: 38 **frozen
`deionizer_reveal_json` captures** in `internal/decompile/testdata/probes/*.json`,
each rendered and compared **byte-for-byte** against a committed golden in
`testdata/golden/`.

- `TestProbeGoldens` — whole-file `RenderFile` output, one golden per construct.
- `TestGoldenMethods` — single-method `Render` path for five control-flow shapes.

Where the runner has a `php`, the same captures are also graded on the two tiers
the local matrix uses, which is what makes the online suite more than a diff:

- `TestRenderedFilesLint` — `php -l` on every render (38/38). A golden freezes
  broken syntax happily; this is the failure mode it cannot catch.
- `TestRenderedFilesBehavior` — runs the render **and** the plaintext fixture it
  was captured from through `tests/driver/run_probe.php` (the harness's own
  name-independent driver, which picks the entry point by shape, not by name) and
  requires byte-identical stdout. Currently 34 pass, 3 are lint-only (no plaintext
  original committed), 1 is a recorded gap.

Both render through `RenderFile` **at test time** rather than reading the golden,
so they grade today's renderer — a change that updates a golden but breaks the
semantics still fails. `chore test:php` runs just these two.
`DEIONIZER_REQUIRE_PHP=1` turns "no interpreter" from a skip into a failure; CI
sets it on the Linux leg, so the tiers can never silently vanish.

`knownBehaviorGaps` in `behavior_test.go` records real decompiler bugs so the
suite is green-when-correct without lying. A gap that starts passing **fails** the
test: fix the bug and delete the entry in the same commit. Do not add an entry to
silence a regression — an entry is a bug report, not a waiver.

These captures are of **our own fixtures** in [tests/fixtures/](tests/fixtures/),
so every golden is checkable against its own plaintext source. A rendering
regression that would flip a harness cell to FAIL turns `go test` red here first,
with no Docker and no vendor assets involved.

**Regenerating goldens is a deliberate act:**

```sh
GOLDEN_UPDATE=1 go test ./internal/decompile/    # then READ the diff
```

A golden freezes current behaviour; it does not prove it correct. Never regenerate
to make a red test green — that converts a regression into a committed expectation.

### Tier 2 — the correctness matrix, local only

```sh
chore test:matrix                          # full sweep
chore test:matrix versions="7.4 8.1"       # filter version
chore test:matrix methods=plain fixtures=for_loop
chore test:smoke                           # single cell, 7.4/plain
chore dashboard                            # print the last run's matrix
```

Real **encode → decode → run**: each fixture is encoded with the ionCube trial
encoder, decoded by `bin/deionizer`, then graded at three tiers — *artifact*
produced, *lint* clean (`php -l`), *behavior* (decoded output's stdout matches the
original's, byte for byte). Behavior is the headline number.

Needs Docker, a built runtime+decode image per version, and the trial encoder under
`examples/ioncube-trial/` — none of which exists on a GitHub runner, which is why
`tests/run.sh` is never invoked by CI.

Outputs `tests/results/results.json` + `dashboard.md`. **Both are gitignored and
must stay that way** — they are per-operator artifacts describing whichever corpus
and versions that person happened to run. When quoting a rate, quote the `generated`
timestamp, the versions and the cell count with it; a 6-cell smoke and a 119-cell
sweep both print a percentage.

---

## 6. Repository rules

**Never commit:** the ionCube loader, the decode-extension source, the trial
encoder, any customer corpus, or generated sweep results. `/examples`, `/fixtures`,
`/tmp`, `/scratchpad`, `/bin`, `/dist` are gitignored for exactly this reason, and
the `git-block-private-paths` / `git-block-large-files` pre-commit guards enforce it.

**`runtimes.yml` exists twice** — the root copy and the `go:embed`-ed
`internal/runtime/runtimes.yml`. Edit both, then `chore check:runtimes`. Comments
may diverge; data may not.

**Branch, PR, squash.** `main` takes no direct pushes and no merge commits. Work on
a branch, open a **draft** PR, let the `CI` check go green, mark it ready, squash
merge. The active guards:

| Guard | When | What it blocks |
|---|---|---|
| `github-protect-main` | pre-commit | direct commits/pushes to the default branch |
| `github-merge-squash-only` | pre-commit | non-squash merge settings |
| `git-block-merge-commits` | pre-push | a local merge commit reaching the remote |
| `git-block-private-paths`, `git-block-large-files`, `git-block-bad-files` | pre-commit | vendor assets, corpora, junk |
| `git-no-trailing-whitespace` | pre-commit | trailing whitespace |
| `git-changelog` | pre-push | a version tag whose release is not in `CHANGELOG.md` |
| `git-tags-on-main` | pre-push | a tag on a commit that never landed on main |

`.githooks/required-checks` names the one status check main must pass: `CI`.
Bypass is `--no-verify` and needs a reason you would say out loud.

Commit messages are conventional (`feat:`, `fix:`, `chore:`, `docs:`, `style:`).

---

## 7. CI — [.github/workflows/ci.yml](.github/workflows/ci.yml)

On every PR and every push to `main`, on `ubuntu-latest` **and** `macos-latest`:

1. **Lint workflows** — actionlint, fetched as a release binary and verified against
   the published sha256 (ubuntu leg only; this is why no third-party action is used).
2. **Formatting** — `gofmt -l .` must be empty.
3. **Vet** — `go vet ./...`.
4. **Ensure PHP** — ubuntu leg only, so the lint/behavior tiers are guaranteed to
   run there (`DEIONIZER_REQUIRE_PHP=1` makes their absence fatal).
5. **Test** — `go test -race -count=1 ./...`.
6. **Build** — build and run `--version`.

Then one always-run aggregate job named **`CI`** that `needs:` the matrix. Branch
protection requires *that* name only, so matrix legs can be renamed or added
without stranding a merge. A skipped leg satisfies it; a failed one fails it.

Every action is pinned to a commit SHA. Keep it that way.

---

## 8. Releasing, and getting it to `brew`

The two repositories are deliberately decoupled: this one **publishes releases**;
the tap **pulls** from them under human review. No credential here can write to the
tap, and `goreleaser`'s `brews:` block is intentionally unused.

### 8.1 Cut the release

1. Land everything on `main` through a PR (§6).
2. **Write the `CHANGELOG.md` section first.** Add `## vX.Y.Z` with the real
   changes. The `git-changelog` pre-push guard reads the *tagged commit's* files
   and refuses the tag if that section is missing — and it is also the source the
   release notes are taken from (`.githooks/pre-push.d/git-changelog.sh notes vX.Y.Z`).
3. Tag the commit on `main` and push the tag:

   ```sh
   git checkout main && git pull --ff-only
   git tag -a v0.1.1 -m "v0.1.1"
   git push origin v0.1.1
   ```

   `git-tags-on-main` blocks a tag that is not contained in `main`.

### 8.2 What [release.yml](.github/workflows/release.yml) then does

Tag push (or `workflow_dispatch` **with the tag selected as the ref** — there is no
version input, the version comes from the tag) runs:

1. **goreleaser** `release --clean`, pinned to `GORELEASER_VERSION` (currently
   `v2.17.0`) with `GOTOOLCHAIN=go1.25.0`. Builds `darwin,linux × amd64,arm64`,
   `CGO_ENABLED=0`, `-trimpath -buildvcs=false -s -w`, version and `.CommitDate`
   stamped via ldflags, `mod_timestamp` from the commit. Artifacts are named
   `deionizer-<version>-<os>-<arch>.tar.gz` with `x86_64` for amd64 — **the tap's
   formula depends on those exact names.**
2. **Check the version stamp** — runs the linux/amd64 binary and requires
   `--version` to print the tag minus its `v`.
3. **Publish binary checksums** — unpacks every archive and uploads `binaries.txt`
   (hashes of the *binaries*, not the archives, since tar records mtimes).
4. **Reproduce the release independently** — re-fetches the same pinned goreleaser
   (verified against its own published checksums), rebuilds from the same commit,
   and fails if a single byte differs from what was just published.

If step 4 ever fails, the cause is something non-deterministic entering the build —
a wall-clock timestamp, an absolute path, an unpinned toolchain. Fix the cause;
do not delete the check.

### 8.3 Publish to Homebrew

In [antimatter-studios/homebrew-tap](https://github.com/antimatter-studios/homebrew-tap):

1. Run the **"Sync formulae from releases"** workflow. It reads this repo's public
   releases, downloads the assets, computes their checksums itself, and opens an
   **unmerged PR** against `Formula/deionizer.rb`.
2. Review the version + four sha256 values, then merge.

Users then get it with:

```sh
brew install antimatter-studios/tap/deionizer
deionizer --version        # must print the released version
```

The formula installs the bare `deionizer` binary and its `test do` block runs
`--version` on the user's machine — which is where a broken darwin cross-build
would surface, since the release job can only execute the linux one.

### 8.4 Current state

Tag `v0.1.0` is released and in the tap. `CHANGELOG.md` already carries a
**`v0.1.1`** section (bring-your-own loader, `--loader-dir` replacing
`--loader-base-url`) that has **never been tagged** — so §8.1 step 2 is already
done for it and the next release is a tag push away.

---

## 9. Hard rules for an agent working here

1. **Do not weaken a check to get green.** Not the gofmt step, not the
   reproducibility step, not a golden. Fix the cause.
2. **Do not regenerate goldens to silence a failure** (§5).
3. **Do not commit vendor or customer material** (§6). If a path is gitignored,
   that is a decision, not an oversight.
4. **Do not push to `main`, and do not open a non-draft PR** — draft first, CI
   green, then ready + squash.
5. **Do not add a step that writes to the Homebrew tap from here** (§8).
6. **Quote test numbers with their provenance** — timestamp, versions, cell count.
   `tests/results/` is one operator's local run, not the project's score.
7. **Prefer `chore <task>` over a raw command**, so the guards and the documented
   path stay the same for everyone.
