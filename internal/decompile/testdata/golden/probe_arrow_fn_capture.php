<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function probe() {
    $base = 100;
    $step = 5;
    $f = fn ($x) => $x + $base;
    $g = fn ($x) => fn ($y) => $x + $y + $base;
    $nums = array_map(fn ($n) => $n * $step, [1, 2, 3]);
    return $f(1) . '|' . $g(10)(20) . '|' . implode(',', $nums);
}
