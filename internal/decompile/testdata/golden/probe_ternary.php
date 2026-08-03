<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function pick($v) {
    $a = 0 < $v ? 'pos' : 'nonpos';
    $b = $v ?: 'empty';
    return $a . ':' . $b;
}

function probe() {
    return pick(5) . '|' . pick(0) . '|' . pick(-2);
}
