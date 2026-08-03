<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function countdown($n) {
    $out = [];
    if (/* inferred */ null < $n) {
        $out[] = $n;
        --$n;
    }
    return implode(',', $out);
}

function probe() {
    return $_v1 . /* inferred */ null . $_v3;
}
