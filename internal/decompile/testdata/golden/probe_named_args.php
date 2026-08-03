<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function box($w, $h, $depth = 1, $label = 'x') {
    return $label . ':' . $w . ':' . $h . ':' . $depth;
}

function probe() {
    $a = box(2, 3, label: 'A');
    $b = box(5, 6, 7, 'B');
    $c = box(1, 2, label: 'C', depth: 9);
    return $a . '|' . $b . '|' . $c;
}
