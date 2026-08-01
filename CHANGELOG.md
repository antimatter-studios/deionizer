# Changelog

## v0.1.0

Initial release.

Recover readable, runnable PHP from ionCube-encoded code you own: deionizer runs each
file through a version-matched loader in a throwaway container and reads back what the
runtime restores — interface skeletons via reflection, method bodies via the
deep-decode path. No decryption, no bundled vendor loader or key.

- `deionizer process <tree>` — auto-detect each project's PHP version and full-decode
  it in place (`<file>.decoded-source.php`).
- `deionizer decode <input> [--output DIR]` — force deep decode: a tree in place, a
  single file or URL to stdout, or `--output` to mirror into DIR (source untouched)
  with a per-file confidence report (`confidence-report.json` + `.txt`).
- `deionizer --skeleton …` — fast interface-only recovery: exact class/method/property
  signatures via the loader, no bodies.
- `deionizer build` / `shell` / `version`.
- Deployment config is passed on the command line, never read from the environment
  (`.env` → `chore` → `--flags` → binary), so the path from config to behaviour is
  explicit and traceable.
- Reproducible release: the pipeline rebuilds every published binary from the tagged
  commit and fails if a single byte differs.
