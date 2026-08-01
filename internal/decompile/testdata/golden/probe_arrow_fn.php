<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function probe() {
    $factor = 3;
    $f = fn ($x) => $x * $factor;
    $nums = array_map(fn ($n) => $n + 1, [1, 2, 3]);
    return $f(4) . '|' . implode(',', $nums);
}
