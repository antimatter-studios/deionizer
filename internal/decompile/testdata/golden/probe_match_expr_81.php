<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function grade($score) {
    return match (true) {
        90 <= $score => 'A',
        80 <= $score => 'B',
        70 <= $score => 'C',
        default => 'F',
    };
}

function probe() {
    return grade(95) . '|' . grade(83) . '|' . grade(50);
}
