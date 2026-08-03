<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function countdown($n) {
    $out = [];
    while (0 < $n) {
        $out[] = $n;
        $n--;
    }
    return implode(',', $out);
}

function probe() {
    return countdown(4) . '|' . countdown(0);
}
