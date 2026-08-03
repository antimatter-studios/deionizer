# 19 — Decoder correctness test matrix

Generated: 2026-07-31T11:35:54+02:00
Decoder: `bin/deionizer`
Encoder: ionCube PHP Encoder 15.0 (evaluation), ioncube_encoderNN_15.0_64
Method: encode each ground-truth fixture with the official ionCube trial encoder, decode with `ico decode`, then diff decoded-vs-original at three tiers — **artifact** produced, **lint** clean (`php -l`), and **behavior** (decoded reproduces the original's runtime output byte-for-byte). Behavior is the headline metric; every PASS is a real encode -> decode -> run diff.

## Headline

> **Behavioral success rate: 15.8%** (12 / 76 applicable cells)

| Tier | Passing | Of applicable | Rate |
|---|---|---|---|
| Artifact produced | 76 | 76 | 100.0% |
| Lints clean (`php -l`) | 63 | 76 | 82.9% |
| **Behavior matches** | **12** | **76** | **15.8%** |

**Interpretation.** The three tiers separate *structure* recovery from *behavior* recovery. A high artifact/lint rate with a low behavior rate means the decoder reconstructs the shape of the code (signatures, control-flow skeleton) but the reconstructed **bodies do not yet compute the right result** — the decompiler's opcode→PHP lift is still lossy (operators, constants and data-flow are frequently wrong; on PHP 5.x scalar literals are lost entirely). The behavior tier is therefore the honest "how close to a usable decompile" number.

### By encoding method

| Method | Behavior pass | Rate |
|---|---|---|
| plain | 6 / 38 | 15.8% |
| optimise-max | 6 / 38 | 15.8% |

_`plain` and `optimise-none` encode identically: the encoder rejects an explicit `--optimise none`, so omitting the flag IS the no-optimisation baseline. `obfuscate-all` uses `--obfuscate all --obfuscation-key`; `dynamic-keys` is the annotated-fixture observation below (its full output cannot be reproduced by design — the protected bodies stay encrypted)._

### By PHP version

| PHP | Behavior pass | Rate |
|---|---|---|
| 8.1 | 6 / 38 | 15.8% |
| 8.3 | 6 / 38 | 15.8% |

## Behavior matrix (construct x version x method)

Behavior is graded name-independently: the decoded artifact is run as a whole program and its stdout diffed against the original's — no hardcoded entry-symbol name (see `tests/driver/run_probe.php`).

Legend: PASS = decoded output ran identically to original · FAIL = ran but differed / no output · `-` = decoder emitted no artifact · `n/a` = construct not available in that PHP version.

| Construct | 8.1 / plain | 8.1 / optimise-max | 8.3 / plain | 8.3 / optimise-max |
|---|---|---|---|---|
| arrow fns capturing vars | FAIL | FAIL | FAIL | FAIL |
| class constants + ::class | FAIL | FAIL | FAIL | FAIL |
| closures with use(&$ref) | FAIL | FAIL | FAIL | FAIL |
| constructor property promotion | FAIL | FAIL | FAIL | FAIL |
| enums (pure + backed) | FAIL | FAIL | FAIL | FAIL |
| first-class callable syntax | FAIL | FAIL | FAIL | FAIL |
| generators (yield, yield from) | FAIL | FAIL | FAIL | FAIL |
| heredoc + nowdoc | PASS | PASS | PASS | PASS |
| interfaces + abstract methods | FAIL | FAIL | FAIL | FAIL |
| list/[] destructuring (keyed, nested) | FAIL | FAIL | FAIL | FAIL |
| match expression | FAIL | FAIL | FAIL | FAIL |
| multi-catch (A|B $e) + finally | FAIL | FAIL | FAIL | FAIL |
| named arguments | FAIL | FAIL | FAIL | FAIL |
| nullsafe operator ?-> | FAIL | FAIL | FAIL | FAIL |
| references in foreach | PASS | PASS | PASS | PASS |
| static props + late static binding | FAIL | FAIL | FAIL | FAIL |
| string interpolation forms | FAIL | FAIL | FAIL | FAIL |
| traits + conflict resolution | FAIL | FAIL | FAIL | FAIL |
| variadics (...$a) + spread | PASS | PASS | PASS | PASS |

## Lint matrix (decoded output is syntactically valid PHP)

| Construct | 8.1 / plain | 8.1 / optimise-max | 8.3 / plain | 8.3 / optimise-max |
|---|---|---|---|---|
| arrow fns capturing vars | ok | ok | ok | ok |
| class constants + ::class | ok | ok | ok | ok |
| closures with use(&$ref) | ok | ok | ok | ok |
| constructor property promotion | ok | ok | ok | ok |
| enums (pure + backed) | ok | ok | err | err |
| first-class callable syntax | ok | ok | ok | ok |
| generators (yield, yield from) | ok | ok | ok | ok |
| heredoc + nowdoc | ok | ok | ok | ok |
| interfaces + abstract methods | ok | ok | err | ok |
| list/[] destructuring (keyed, nested) | ok | ok | ok | ok |
| match expression | err | err | err | err |
| multi-catch (A|B $e) + finally | ok | ok | ok | ok |
| named arguments | ok | ok | ok | ok |
| nullsafe operator ?-> | ok | ok | err | err |
| references in foreach | ok | ok | ok | ok |
| static props + late static binding | err | err | err | err |
| string interpolation forms | ok | ok | ok | ok |
| traits + conflict resolution | ok | ok | ok | ok |
| variadics (...$a) + spread | ok | ok | ok | ok |

## Top failing constructs

| Construct | Behavior FAIL / applicable | Representative decoded output |
|---|---|---|
| arrow fns capturing vars | 4 / 4 | exp `101\|130\|5,10,15` -> got `(empty)` |
| class constants + ::class | 4 / 4 | exp `2.0\|a,b\|Widget\|iface\|Widget` -> got `(empty)` |
| closures with use(&$ref) | 4 / 4 | exp `123\|7` -> got `111\|0` |
| constructor property promotion | 4 / 4 | exp `A(3,4)\|p(0,9)\|3,9` -> got `A(3,4)\|p(9,0)\|3,0` |
| enums (pure + backed) | 4 / 4 | exp `H,red\|Spades,black\|on\|2` -> got `(empty)` |
| first-class callable syntax | 4 / 4 | exp `11\|10\|12\|5\|2,4,6` -> got `(empty)` |
| generators (yield, yield from) | 4 / 4 | exp `start,a,b,end\|1:1,2:4,3:9` -> got `\|` |
| interfaces + abstract methods | 4 / 4 | exp `shape:Square=16\|3\|yes` -> got `shape:Square=16\|3\|no` |
| list/[] destructuring (keyed, nested) | 4 / 4 | exp `1234\|10,20\|kept\|78\|1=one,2=two` -> got `1234\|10,20\|kept\|78\|=,=` |
| match expression | 4 / 4 | exp `neg,zero,small,big\|1120` -> got `(empty)` |
| multi-catch (A|B $e) + finally | 4 / 4 | exp `caught:RuntimeException:R,fin\|caught:LogicExcep…` -> got `fin\|caught:LogicException:L,fin\|ok,fin` |
| named arguments | 4 / 4 | exp `A:2:3:1\|B:5:6:7\|C:1:2:9` -> got `x:2:3:A\|B:5:6:7\|9:1:2:C` |
| nullsafe operator ?-> | 4 / 4 | exp `ROME\|NULL\|none` -> got `(empty)` |
| static props + late static binding | 4 / 4 | exp `user#1,user#2,base#1\|2\|base` -> got `(empty)` |
| string interpolation forms | 4 / 4 | exp `obj=Y\|simpleArr=Q\|idxArr=Z\|method=M\|prop=Y e…` -> got `obj=\|simpleArr=Q\|idxArr=Z\|method=M\|prop= end…` |
| traits + conflict resolution | 4 / 4 | exp `hello\|world\|B` -> got `(empty)` |

## Reproduce

```sh
./tests/run.sh                                  # full sweep (all methods x versions)
OBF=none VERSIONS=7.4 ./tests/run.sh            # quick single-cell smoke (plain)
METHODS="plain optimise-max" VERSIONS=7.4 ./tests/run.sh   # pick methods
```

Machine-readable results: `tests/results/results.json`.
