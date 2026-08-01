package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// writeDashboard renders the pass/fail matrix + success rate to a Markdown file.
func writeDashboard(path string, r Results) {
	var b strings.Builder

	w := func(f string, a ...interface{}) { fmt.Fprintf(&b, f, a...) }

	w("# 19 — Decoder correctness test matrix\n\n")
	w("Generated: %s  \n", r.Generated)
	w("Decoder: `%s`  \n", r.Decoder)
	w("Encoder: %s  \n", r.Encoder)
	w("Method: encode each ground-truth fixture with the official ionCube trial encoder, ")
	w("decode with `deionizer decode`, then diff decoded-vs-original at three tiers — ")
	w("**artifact** produced, **lint** clean (`php -l`), and **behavior** (decoded reproduces ")
	w("the original's runtime output byte-for-byte). Behavior is the headline metric; every ")
	w("PASS is a real encode -> decode -> run diff.\n\n")

	// Headline.
	s := r.Summary
	w("## Headline\n\n")
	w("> **Behavioral success rate: %.1f%%** (%d / %d applicable cells)\n\n", s.SuccessRatePct, s.BehaviorPass, s.ApplicableCells)
	w("| Tier | Passing | Of applicable | Rate |\n|---|---|---|---|\n")
	w("| Artifact produced | %d | %d | %.1f%% |\n", s.ArtifactCount, s.ApplicableCells, pct(s.ArtifactCount, s.ApplicableCells))
	w("| Lints clean (`php -l`) | %d | %d | %.1f%% |\n", s.LintPass, s.ApplicableCells, pct(s.LintPass, s.ApplicableCells))
	w("| **Behavior matches** | **%d** | **%d** | **%.1f%%** |\n\n", s.BehaviorPass, s.ApplicableCells, s.SuccessRatePct)

	w("**Interpretation.** The three tiers separate *structure* recovery from *behavior* ")
	w("recovery. A high artifact/lint rate with a low behavior rate means the decoder ")
	w("reconstructs the shape of the code (signatures, control-flow skeleton) but the ")
	w("reconstructed **bodies do not yet compute the right result** — the decompiler's ")
	w("opcode→PHP lift is still lossy (operators, constants and data-flow are frequently ")
	w("wrong; on PHP 5.x scalar literals are lost entirely). The behavior tier is therefore ")
	w("the honest \"how close to a usable decompile\" number.\n\n")

	// Breakdown by encoding method (the widened axis) — the per-method summary.
	w("### By encoding method\n\n| Method | Behavior pass | Rate |\n|---|---|---|\n")
	for _, m := range r.Methods {
		p := s.ByMethod[m]
		w("| %s | %d / %d | %.1f%% |\n", m, p.Pass, p.Total, p.Pct)
	}
	if r.Dynkeys != nil && r.Dynkeys.Version != "" {
		dp := 0
		if r.Dynkeys.Behavior == "pass" {
			dp = 1
		}
		w("| dynamic-keys | %d / 1 | %.1f%% |\n", dp, pct(dp, 1))
	}
	w("\n_`plain` and `optimise-none` encode identically: the encoder rejects an ")
	w("explicit `--optimise none`, so omitting the flag IS the no-optimisation ")
	w("baseline. `obfuscate-all` uses `--obfuscate all --obfuscation-key`; ")
	w("`dynamic-keys` is the annotated-fixture observation below (its full output ")
	w("cannot be reproduced by design — the protected bodies stay encrypted)._\n\n")

	w("### By PHP version\n\n| PHP | Behavior pass | Rate |\n|---|---|---|\n")
	for _, v := range r.Versions {
		p := s.ByVersion[v]
		w("| %s | %d / %d | %.1f%% |\n", v, p.Pass, p.Total, p.Pct)
	}
	w("\n")

	// Columns = version x method.
	type col struct{ ver, method, label string }
	var cols []col
	for _, v := range r.Versions {
		for _, m := range r.Methods {
			cols = append(cols, col{v, m, v + " / " + m})
		}
	}

	// index cells by construct -> "ver|method".
	idx := map[string]map[string]Cell{}
	constructs := []string{}
	for _, c := range r.Cells {
		if _, ok := idx[c.Construct]; !ok {
			idx[c.Construct] = map[string]Cell{}
			constructs = append(constructs, c.Construct)
		}
		idx[c.Construct][c.Version+"|"+c.Method] = c
	}
	sort.Strings(constructs)

	// Matrix: behavior.
	w("## Behavior matrix (construct x version x method)\n\n")
	w("Behavior is graded name-independently: the decoded artifact is run as a whole program and its ")
	w("stdout diffed against the original's — no hardcoded entry-symbol name (see `tests/driver/run_probe.php`).\n\n")
	w("Legend: PASS = decoded output ran identically to original · FAIL = ran but differed / no output · ")
	w("`-` = decoder emitted no artifact · `n/a` = construct not available in that PHP version.\n\n")
	w("| Construct |")
	for _, cl := range cols {
		w(" %s |", cl.label)
	}
	w("\n|---|")
	for range cols {
		w("---|")
	}
	w("\n")
	for _, name := range constructs {
		w("| %s |", name)
		for _, cl := range cols {
			w(" %s |", behaviorSym(idx[name][cl.ver+"|"+cl.method]))
		}
		w("\n")
	}
	w("\n")

	// Secondary matrix: lint (shows how far output gets even when behavior fails).
	w("## Lint matrix (decoded output is syntactically valid PHP)\n\n")
	w("| Construct |")
	for _, cl := range cols {
		w(" %s |", cl.label)
	}
	w("\n|---|")
	for range cols {
		w("---|")
	}
	w("\n")
	for _, name := range constructs {
		w("| %s |", name)
		for _, cl := range cols {
			w(" %s |", lintSym(idx[name][cl.ver+"|"+cl.method]))
		}
		w("\n")
	}
	w("\n")

	// Top failing constructs (by count of applicable non-passing cells).
	type fc struct {
		name          string
		fails, applic int
	}
	var fails []fc
	for _, name := range constructs {
		f := fc{name: name}
		for _, cl := range cols {
			c := idx[name][cl.ver+"|"+cl.method]
			if !c.Applicable {
				continue
			}
			f.applic++
			if c.Behavior != "pass" {
				f.fails++
			}
		}
		if f.fails > 0 {
			fails = append(fails, f)
		}
	}
	sort.Slice(fails, func(i, j int) bool {
		if fails[i].fails != fails[j].fails {
			return fails[i].fails > fails[j].fails
		}
		return fails[i].name < fails[j].name
	})
	w("## Top failing constructs\n\n")
	if len(fails) == 0 {
		w("None — every applicable construct reproduced its original behavior.\n\n")
	} else {
		w("| Construct | Behavior FAIL / applicable | Representative decoded output |\n|---|---|---|\n")
		for _, f := range fails {
			var rep Cell
			for _, cl := range cols {
				c := idx[f.name][cl.ver+"|"+cl.method]
				if c.Applicable && c.Behavior != "pass" {
					rep = c
					break
				}
			}
			w("| %s | %d / %d | exp `%s` -> got `%s` |\n",
				f.name, f.fails, f.applic, oneLine(rep.Expected), oneLine(rep.Actual))
		}
		w("\n")
	}

	// Pending versions.
	if len(r.Pending) > 0 {
		w("## Pending versions\n\n")
		for _, p := range r.Pending {
			w("- **PHP %s** — %s. Activates automatically once a `decode_image` is set for it in `runtimes.yml`.\n", p.Version, p.Reason)
		}
		w("\n")
	}

	// Dynamic keys observation.
	if r.Dynkeys != nil {
		dk := r.Dynkeys
		w("## Dynamic Keys observation (probe-dynkeys.php)\n\n")
		w("Encoded with source `// @ioncube.dk … RANDOM` annotations (no CLI obfuscation), then decoded. ")
		w("Recorded for observation only — no evasion built.\n\n")
		w("- Encoded successfully: **%v**", dk.Encoded)
		if dk.EncodeNote != "" {
			w(" — %s", dk.EncodeNote)
		}
		w("\n")
		w("- Decoder produced artifact: **%v**\n", dk.Artifact)
		w("- %s\n", dk.DecodeNote)
		if dk.Version != "" {
			w("- Whole-program behavior on PHP %s (name-independent): **%s** — exp `%s` vs got `%s`\n",
				dk.Version, dk.Behavior, oneLine(dk.Expected), oneLine(dk.Actual))
		}
		if len(dk.DkProtected) > 0 {
			w("- Dynamic-key-protected methods (should stay hidden): `%s`\n", strings.Join(dk.DkProtected, "`, `"))
		}
		if dk.Artifact {
			w("- Recovered code length: %d bytes\n\n", dk.RecoveredLen)
			// Per-method recovery table.
			var names []string
			for m := range dk.Methods {
				names = append(names, m)
			}
			sort.Strings(names)
			w("| Method | Dynamic-key protected | Body recovered by passive reveal |\n|---|---|---|\n")
			for _, m := range names {
				prot := contains(dk.DkProtected, m)
				w("| `%s` | %s | %s |\n", m, yesno(prot), yesno(dk.Methods[m]))
			}
			w("\n**Finding:** the dynamic-key-protected bodies are **not** recovered by passive ")
			w("load-and-reveal — ionCube leaves them encrypted until first call with the correct ")
			w("runtime key, so nothing to lift is present. Only non-protected members yield (still ")
			w("lossy) bodies. This is the intended property of Dynamic Keys.\n\n")
			w("Recovered source:\n\n```php\n%s\n```\n\n", dk.Snippet)
		} else {
			w("\n")
		}
	}

	w("## Reproduce\n\n")
	w("```sh\n./tests/run.sh                                  # full sweep (all methods x versions)\n")
	w("OBF=none VERSIONS=7.4 ./tests/run.sh            # quick single-cell smoke (plain)\n")
	w("METHODS=\"plain optimise-max\" VERSIONS=7.4 ./tests/run.sh   # pick methods\n```\n\n")
	w("Machine-readable results: `tests/results/results.json`.\n")

	os.MkdirAll(dirOf(path), 0o755)
	os.WriteFile(path, []byte(b.String()), 0o644)
}

func behaviorSym(c Cell) string {
	if !c.Applicable {
		if c.Status == "pending" {
			return "pending"
		}
		return "n/a"
	}
	switch c.Behavior {
	case "pass":
		return "PASS"
	case "error":
		return "FAIL*" // fixture produced no ground truth for this version
	default:
		if !c.Artifact {
			return "-"
		}
		return "FAIL"
	}
}

func lintSym(c Cell) string {
	if !c.Applicable {
		return "n/a"
	}
	if !c.Artifact {
		return "-"
	}
	switch c.Lint {
	case "pass":
		return "ok"
	case "fail":
		return "err"
	default:
		return "-"
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "|", "\\|")
	if len(s) > 48 {
		s = s[:48] + "…"
	}
	if s == "" {
		return "(empty)"
	}
	return s
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return "."
}
