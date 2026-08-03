<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function joiner($sep, ...$parts) {
    return implode($sep, $parts);
}

function probe() {
    $nums = [1, 2, 3, 4];
    return joiner('-', 'a', 'b', 'c') . '|' . joiner(',', ...$nums);
}
