<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function makeAdder($base) {
    return fn ($x) => $base + $x;
}

function probe() {
    $add10 = makeadder(10);
    $mul = fn ($a, $b) => $a * $b;
    return $add10(5) . '|' . $mul(3, 4);
}
