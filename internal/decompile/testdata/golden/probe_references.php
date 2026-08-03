<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function normalize($xs) {
    foreach ($xs as &$x) {
        $x = $x * 10;
    }
    unset($x);
    $a = 1;
    $b = &$a;
    $b = 7;
    return implode(',', $xs) . '|' . $a;
}

function probe() {
    return normalize([1, 2, 3]);
}
