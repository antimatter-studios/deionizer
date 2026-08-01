#!/usr/bin/env bash
# guard: git-block-private-paths
# Refuse to commit anything whose path starts with one of the repo's private,
# do-not-publish top-level directories. These hold reverse-engineering source and
# dumps, third-party sample corpora, and the ionCube encoder — none of it belongs
# in the published repo. .gitignore already keeps them out, but `git add -f` walks
# straight through .gitignore; this guard is the hard backstop so that material
# cannot be committed mechanically, by accident or on purpose. Blocks (exit 1).
#
# Matches STAGED added/copied/modified paths (relative to the repo root) whose
# first path component starts with one of PREFIXES below. Add a directory here to
# extend the wall. For a genuine, intentional edge: git commit --no-verify.
set -uf

# Private top-level path prefixes. A staged path is blocked when it begins with
# any of these (so `tmp/…`, `examples/…`, `fixtures/…`, and `tmp*`/`examples*`/
# `fixtures*` top-level files are all caught).
PREFIXES='tmp examples fixtures'

staged=$(git diff --cached --name-only --diff-filter=ACM)
[ -n "$staged" ] || exit 0

fail=0
while IFS= read -r path; do
  [ -n "$path" ] || continue
  for pre in $PREFIXES; do
    case "$path" in
      "$pre"*)
        echo "github-guard: refusing to commit '$path' — it is under the private '$pre' area, which must not be published." >&2
        echo "             unstage it:   git restore --staged \"$path\"" >&2
        echo "             (if this is genuinely intended: git commit --no-verify)" >&2
        fail=1
        break
        ;;
    esac
  done
done <<EOF
$staged
EOF
exit "$fail"
