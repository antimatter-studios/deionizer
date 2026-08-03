<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function fmt($name, $n) {
    $arr = ['k' => 'V'];
    $s = 'Hi ' . $name . ', n=' . $n;
    $t = 'name=' . $name . ' count=' . $n . ' key=' . $arr['k'];
    return $s . '|' . $t;
}

function probe() {
    return fmt('Sam', 3);
}
