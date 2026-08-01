<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function doubler($xs) {
    $out = [];
    foreach ($xs as $x) {
        $out[] = $x * 2;
    }
    return implode(',', $out);
}

function kv($m) {
    $out = [];
    foreach ($m as $k => $v) {
        $out[] = $k . '=' . $v;
    }
    return implode(',', $out);
}

function probe() {
    return doubler([1, 2, 3]) . '|' . kv(['a' => 1, 'b' => 2]);
}
