# 19 — Decoder correctness test matrix

Generated: 2026-07-31T11:31:17+02:00
Decoder: `bin/deionizer`
Encoder: ionCube PHP Encoder 15.0 (evaluation), ioncube_encoderNN_15.0_64
Method: encode each ground-truth fixture with the official ionCube trial encoder, decode with `ico decode`, then diff decoded-vs-original at three tiers — **artifact** produced, **lint** clean (`php -l`), and **behavior** (decoded reproduces the original's runtime output byte-for-byte). Behavior is the headline metric; every PASS is a real encode -> decode -> run diff.

## Headline

> **Behavioral success rate: 23.1%** (6 / 26 applicable cells)

| Tier | Passing | Of applicable | Rate |
|---|---|---|---|
| Artifact produced | 26 | 26 | 100.0% |
| Lints clean (`php -l`) | 24 | 26 | 92.3% |
| **Behavior matches** | **6** | **26** | **23.1%** |

**Interpretation.** The three tiers separate *structure* recovery from *behavior* recovery. A high artifact/lint rate with a low behavior rate means the decoder reconstructs the shape of the code (signatures, control-flow skeleton) but the reconstructed **bodies do not yet compute the right result** — the decompiler's opcode→PHP lift is still lossy (operators, constants and data-flow are frequently wrong; on PHP 5.x scalar literals are lost entirely). The behavior tier is therefore the honest "how close to a usable decompile" number.

### By encoding method

| Method | Behavior pass | Rate |
|---|---|---|
| plain | 3 / 13 | 23.1% |
| optimise-max | 3 / 13 | 23.1% |

_`plain` and `optimise-none` encode identically: the encoder rejects an explicit `--optimise none`, so omitting the flag IS the no-optimisation baseline. `obfuscate-all` uses `--obfuscate all --obfuscation-key`; `dynamic-keys` is the annotated-fixture observation below (its full output cannot be reproduced by design — the protected bodies stay encrypted)._

### By PHP version

| PHP | Behavior pass | Rate |
|---|---|---|
| 7.4 | 6 / 26 | 23.1% |

## Behavior matrix (construct x version x method)

Behavior is graded name-independently: the decoded artifact is run as a whole program and its stdout diffed against the original's — no hardcoded entry-symbol name (see `tests/driver/run_probe.php`).

Legend: PASS = decoded output ran identically to original · FAIL = ran but differed / no output · `-` = decoder emitted no artifact · `n/a` = construct not available in that PHP version.

| Construct | 7.4 / plain | 7.4 / optimise-max |
|---|---|---|
| arrow fns capturing vars | FAIL | FAIL |
| class constants + ::class | FAIL | FAIL |
| closures with use(&$ref) | FAIL | FAIL |
| constructor property promotion | n/a | n/a |
| enums (pure + backed) | n/a | n/a |
| first-class callable syntax | n/a | n/a |
| generators (yield, yield from) | FAIL | FAIL |
| heredoc + nowdoc | PASS | PASS |
| interfaces + abstract methods | FAIL | FAIL |
| list/[] destructuring (keyed, nested) | FAIL | FAIL |
| match expression | n/a | n/a |
| multi-catch (A|B $e) + finally | FAIL | FAIL |
| named arguments | n/a | n/a |
| nullsafe operator ?-> | n/a | n/a |
| references in foreach | PASS | PASS |
| static props + late static binding | FAIL | FAIL |
| string interpolation forms | FAIL | FAIL |
| traits + conflict resolution | FAIL | FAIL |
| variadics (...$a) + spread | PASS | PASS |

## Lint matrix (decoded output is syntactically valid PHP)

| Construct | 7.4 / plain | 7.4 / optimise-max |
|---|---|---|
| arrow fns capturing vars | ok | ok |
| class constants + ::class | ok | ok |
| closures with use(&$ref) | ok | ok |
| constructor property promotion | n/a | n/a |
| enums (pure + backed) | n/a | n/a |
| first-class callable syntax | n/a | n/a |
| generators (yield, yield from) | ok | ok |
| heredoc + nowdoc | ok | ok |
| interfaces + abstract methods | ok | ok |
| list/[] destructuring (keyed, nested) | ok | ok |
| match expression | n/a | n/a |
| multi-catch (A|B $e) + finally | ok | ok |
| named arguments | n/a | n/a |
| nullsafe operator ?-> | n/a | n/a |
| references in foreach | ok | ok |
| static props + late static binding | err | err |
| string interpolation forms | ok | ok |
| traits + conflict resolution | ok | ok |
| variadics (...$a) + spread | ok | ok |

## Top failing constructs

| Construct | Behavior FAIL / applicable | Representative decoded output |
|---|---|---|
| arrow fns capturing vars | 2 / 2 | exp `101\|130\|5,10,15` -> got `(empty)` |
| class constants + ::class | 2 / 2 | exp `2.0\|a,b\|Widget\|iface\|Widget` -> got `(empty)` |
| closures with use(&$ref) | 2 / 2 | exp `123\|7` -> got `111\|0` |
| generators (yield, yield from) | 2 / 2 | exp `start,a,b,end\|1:1,2:4,3:9` -> got `\|` |
| interfaces + abstract methods | 2 / 2 | exp `shape:Square=16\|3\|yes` -> got `shape:Square=16\|3\|no` |
| list/[] destructuring (keyed, nested) | 2 / 2 | exp `1234\|10,20\|kept\|78\|1=one,2=two` -> got `1234\|10,20\|kept\|78\|=,=` |
| multi-catch (A|B $e) + finally | 2 / 2 | exp `caught:RuntimeException:R,fin\|caught:LogicExcep…` -> got `fin\|caught:LogicException:L,fin\|ok,fin` |
| static props + late static binding | 2 / 2 | exp `user#1,user#2,base#1\|2\|base` -> got `(empty)` |
| string interpolation forms | 2 / 2 | exp `obj=Y\|simpleArr=Q\|idxArr=Z\|method=M\|prop=Y e…` -> got `obj=\|simpleArr=Q\|idxArr=Z\|method=M\|prop= end…` |
| traits + conflict resolution | 2 / 2 | exp `hello\|world\|B` -> got `(empty)` |

## Reproduce

```sh
./tests/run.sh                                  # full sweep (all methods x versions)
OBF=none VERSIONS=7.4 ./tests/run.sh            # quick single-cell smoke (plain)
METHODS="plain optimise-max" VERSIONS=7.4 ./tests/run.sh   # pick methods
```

Machine-readable results: `tests/results/results.json`.
