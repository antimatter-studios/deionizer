<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function build() {
    $a = ['meta' => ['deep' => ['x' => 9]], 'name' => 'probe', 'nums' => [1, 2, 3]];
    $a['nums'][] = 4;
    return $a['name'] . '|' . implode(',', $a['nums']) . '|' . $a['meta']['deep']['x'];
}

function probe() {
    return build();
}
