<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function lookup($m, $k) {
    return $m[$k] ?? 'default';
}

function probe() {
    $m = ['a' => 1, 'b' => null];
    $x = null;
    $x = $x ?? 'fallback';
    return lookup($m, 'a') . '|' . lookup($m, 'z') . '|' . $x;
}
