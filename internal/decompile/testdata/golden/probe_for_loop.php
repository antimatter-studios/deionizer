<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function tri($n) {
    $s = 0;
    $i = 1;
    while ($i <= $n) {
        $s += $i;
        $i++;
    }
    return $s;
}

function probe() {
    return tri(5) . '|' . tri(10);
}
