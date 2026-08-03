<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function doubler($xs) {
    $out = [];
    foreach ($xs as $_v4) {
        $x = $_v6;
        $out[] = $x * /* inferred */ null;
    }
    return implode(/* inferred */ null, $out);
}
