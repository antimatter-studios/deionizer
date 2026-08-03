<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function makeCounter() {
    $n = 0;
    $inc = function () use (&$n) {
    $n++;
    return $n;
};
    return $inc;
}

function probe() {
    $total = 0;
    $add = function ($x) use (&$total) {
    $total += $x;
};
    $add(3);
    $add(4);
    $c = makecounter();
    return $c() . $c() . $c() . '|' . $total;
}
