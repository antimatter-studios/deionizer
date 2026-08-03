# deionizer

**Recover readable, runnable PHP source from code you own but can no longer read.**

Sometimes the original, human-readable version of some PHP is gone — a backup that
didn't work, a developer who moved on, a project that still runs fine in production
but whose source nobody has anymore. If those files were compiled into ionCube's
encoded form, deionizer helps you get a readable copy back.

It does **not** crack, decrypt, or bypass anything. It runs the code the ordinary
way — through a real, version-matched ionCube loader inside a throwaway container —
and reads back what the runtime itself already exposes: the classes, the methods
and their exact signatures, the constants, and (with the deep-decode extension) the
logic inside. Then it writes that out as clean PHP you can pick up and maintain
again. Think of it as restoring a document from a format you can no longer open,
for a document that was already yours.

Bytecode is architecture-independent, so it runs fine on any host (Apple Silicon,
x86) regardless of where the code originally ran.

## What it recovers

| Recovered exactly | Recovered (best-effort) | Not recovered |
|---|---|---|
| Class / parent / interfaces / abstractness | Method **bodies** (the logic), via the deep-decode extension | Comments |
| Method signatures — names, defaults, by-ref, variadic | 5.x encrypted scalar constants (marked `/* inferred */`) | Original local-variable names inside bodies |
| Constants & their values | | |
| Property visibility / static | | |
| Top-level functions in procedural files | | |

Two depths, same command surface:

| Depth | Command | Needs | Recovers |
|---|---|---|---|
| **Interface** | `chore skeleton <tree>` | just the loader | exact class/method/property **signatures** |
| **Full decode** | `chore process <tree>` | loader **+** decode extension | signatures **and method bodies** |

Every recovered file is written next to the original as `<name>.decoded-source.php`.
To leave the source tree untouched instead, add `--output <dir>`: every file is
decoded into a mirror of that directory, alongside a per-file **confidence report**.

## Requirements

- **Docker** — running, able to pull `php:<ver>-cli` images (and emulate
  `linux/amd64` for PHP < 7.2).
- **Go 1.25+** — to build the CLI.
- **[chore](https://github.com/antimatter-studios/chore)** — the task runner this
  project drives everything through:

  ```bash
  brew install antimatter-studios/tap/chore
  # or
  go install github.com/antimatter-studios/chore@latest
  ```

- A **`.env`** pointing at your own unzipped ionCube loader directory (below).

## Configuration — `.env`

Deployment settings live in a `.env` file. `chore` reads it and passes each value
to the binary **on the command line** — the binary never reads the environment
itself, so the path from your config to the tool is explicit and traceable
(`.env` → `chore` → `--flags` → binary).

```bash
cp .env.example .env      # then fill it in
```

| Variable | Needed for | What it is |
|---|---|---|
| `DEIONIZER_LOADER_DIR` | any image build | Your own unzipped ionCube loader directory. The tool copies the arch+version-matched `ioncube_loader_lin_<ver>.so` from it at build time — it never bundles, hardcodes, or downloads the vendor's loader. You obtain and unzip it yourself. |
| `DEIONIZER_EXT_DIR` | deep decode only | Directory of the decode-extension source. Not shipped with the tool — supply your own; interface recovery needs none of it. |
| `DEIONIZER_GUARD_CONSTS` | some modules | Comma-separated bootstrap guard constants (`NAME` or `NAME=VALUE`) to define so a guarded module will load. |
| `DEIONIZER_RUNTIMES` | optional | Overlay YAML for the runtime matrix. |
| `DEIONIZER_CORPUS_DIR` | tests only | Local corpus for the render-all/coverage tests. |

`.env` is gitignored; `.env.example` is the committed template.

## Quick start

```bash
# 1. build the CLI
chore build

# 2. interface recovery (works with just the loader) — exact signatures, no bodies
chore skeleton /path/to/encoded/src

# 3. full decode (needs DEIONIZER_EXT_DIR set) — signatures AND bodies
chore process /path/to/encoded/src

# decode a single file or URL to stdout (redirect to save)
chore decode /path/to/one.php > one-recovered.php

# decode a whole tree into a mirror + confidence report (source untouched)
chore decode /path/to/encoded/src ./recovered

# open a shell in the matched container to poke around
chore shell /path/to/encoded/src
```

You never pass a PHP version: deionizer auto-detects each file's target from its
ionCube marker, and for version-less markers it trial-loads one file and reads the
loader's own "Encoder for PHP X.Y" verdict.

Run `chore --list` for every task, and `chore <task> --help` for what one takes.
For the full walkthrough and how to read the confidence report, see
[docs/USER-GUIDE.md](docs/USER-GUIDE.md).

## Supported PHP versions

`5.6`, `7.2`, `7.4`, `8.1`, `8.3` — defined in
[`internal/runtime/runtimes.yml`](internal/runtime/runtimes.yml). An encoded file
targeting an in-between version is mapped to the nearest supported runtime. If a
file reports `LOADER-MISMATCH`, it targets a different PHP version than the runtime
you used.

## Project status

Actively developed, MIT-licensed, open source.

- **Interface recovery** works out of the box against every supported version.
- **Deep decode** (method bodies) works with a user-supplied decode extension; a
  RED-GREEN correctness harness under [`tests/`](tests/) measures, per PHP construct
  and version, how close the decoded output is to the original — every pass is a
  real encode → decode → run comparison, not an assertion.

## Developing

Everything goes through `chore`:

```bash
chore build            # build the CLI
chore test             # go unit + golden tests
chore lint             # gofmt check + go vet
chore doctor           # check the toolchain is ready
chore test:matrix      # full correctness sweep (needs Docker + the trial encoder)
```

The first `chore` command you run in a fresh clone activates the repository's git
hooks automatically (a `lifecycle` hook), so the commit/push guards travel with the
repo without any manual setup.

## Scope & license

MIT — see [LICENSE](LICENSE). Point deionizer only at ionCube-encoded code you own
or are authorised to recover. It deliberately does **not** implement a loader-free
or universal decryptor and extracts no vendor key: recovery rides the loader's own
restoration path, so it reaches exactly the code you can already run, and no
further. ionCube and its loader/encoder are the property of ionCube Ltd and are
neither bundled nor redistributed here.
