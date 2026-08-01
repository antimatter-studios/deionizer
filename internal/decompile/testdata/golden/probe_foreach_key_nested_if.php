<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function checkTable($spec) {
    $missing = [];
    foreach ($spec as $col => $type) {
        if ($type === '') {
            $missing[] = $col;
            continue;
        }
        if ($type === 'int' && $col !== 'id') {
            $missing[] = $col . '?';
        }
    }
    return implode(',', $missing);
}

function probe() {
    return checktable(['age' => 'int', 'bio' => 'text', 'id' => 'int', 'name' => '']);
}
