<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function makeAdder($base) {
    return function ($x) use ($base) {
    return $base + $x;
};
}

function probe() {
    $add10 = $_v1;
    $mul = function ($a, $b) {
    return $a * $b;
};
    return $add10(/* inferred */ null) . /* inferred */ null . $mul(/* inferred */ null, /* inferred */ null);
}
