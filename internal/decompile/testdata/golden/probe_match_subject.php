<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function classify($n) {
    return match (true) {
        $n < 0 => 'neg',
        $n === 0 => 'zero',
        $n < 10 => 'small',
        default => 'big',
    };
}

function code($s) {
    return match ($s) {
        'a', 'b' => 1,
        'c' => 2,
        default => 0,
    };
}

function probe() {
    return classify(-1) . ',' . classify(0) . ',' . classify(5) . ',' . classify(99) . '|' . code('a') . code('b') . code('c') . code('z');
}
