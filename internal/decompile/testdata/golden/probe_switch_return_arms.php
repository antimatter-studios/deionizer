<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function monthToNr($name) {
    switch ($name) {
        case 'jan':
        case 'january':
            return 1;
        case 'feb':
        case 'february':
            return 2;
        case 'mar':
        case 'march':
            return 3;
        default:
            return 0;
    }
}

function probe() {
    return monthtonr('jan') . '|' . monthtonr('february') . '|' . monthtonr('march') . '|' . monthtonr('nope');
}
