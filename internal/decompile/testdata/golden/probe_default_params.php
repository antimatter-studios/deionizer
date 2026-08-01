<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function greet($name, $greeting = 'Hello', $punct = '!') {
    return $greeting . ', ' . $name . $punct;
}

function probe() {
    return greet('World') . '|' . greet('Sam', 'Hi') . '|' . greet('A', 'Yo', '?');
}
