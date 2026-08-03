<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function atLeastOnce($n) {
    $out = [];
    $i = 0;
    do {
        $out[] = $i;
        $i++;
    } while ($i < $n);
    return implode(',', $out);
}

function probe() {
    return atleastonce(3) . '|' . atleastonce(0);
}
