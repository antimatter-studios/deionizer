<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function band($v) {
    if ($v < 0) {
        return 'neg';
    }
    if ($v === 0) {
        return 'zero';
    }
    if ($v < 10) {
        return 'small';
    }
    return 'big';
}

function probe() {
    return band(-3) . '|' . band(0) . '|' . band(5) . '|' . band(100);
}
