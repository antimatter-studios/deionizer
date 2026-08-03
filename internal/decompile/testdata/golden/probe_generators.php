<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function inner() {
    yield 'a';
    yield 'b';
}

function outer() {
    yield 'start';
    yield from inner();
    yield 'end';
}

function counter($n) {
    $i = 1;
    while ($i <= $n) {
        yield $i => $i * $i;
        $i++;
    }
}

function probe() {
    $parts = [];
    foreach (outer() as $v) {
        $parts[] = $v;
    }
    $kv = [];
    foreach (counter(3) as $k => $v) {
        $kv[] = $k . ':' . $v;
    }
    return implode(',', $parts) . '|' . implode(',', $kv);
}
