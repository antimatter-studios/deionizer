<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function bump(&$x, $by = 1) {
    $x += $by;
}

function fill(&$acc, $v) {
    $acc[] = $v;
}

function probe() {
    $n = 10;
    bump($n);
    bump($n, 5);
    $a = [];
    fill($a, 'x');
    fill($a, 'y');
    return $n . '|' . implode(',', $a);
}
