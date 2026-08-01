<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function grid($rows) {
    $out = [];
    foreach ($rows as $r => $cols) {
        foreach ($cols as $c) {
            $out[] = $r . ':' . $c;
        }
    }
    return implode(',', $out);
}

function probe() {
    return grid([[1, 2], [3, 4]]);
}
