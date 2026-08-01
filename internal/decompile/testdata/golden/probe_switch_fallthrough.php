<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function classify($code) {
    switch ($code) {
        case 'A':
        case 'B':
            $r = 'ab';
            break;
        case 'C':
            $r = 'c';
        case 'D':
            $r = isset($r) ? $r . 'd' : 'd';
            break;
        default:
            $r = 'other';
    }
    return $r;
}

function probe() {
    return classify('A') . '|' . classify('B') . '|' . classify('C') . '|' . classify('D') . '|' . classify('Z');
}
