<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function probe() {
    $a = [1, 2][0];
    $b = [1, 2][1];
    $c = [3, 4][0];
    $d = [3, 4][1];
    $x = ['x' => 10, 'y' => 20]['x'];
    $y = ['x' => 10, 'y' => 20]['y'];
    $second = ['skip', 'kept'][1];
    $p = [[7], [8]][0][0];
    $q = [[7], [8]][1][0];
    $out = [];
    foreach ([[1, 'one'], [2, 'two']] as [$num, $word]) {
        $out[] = $num . '=' . $word;
    }
    return $a . $b . $c . $d . '|' . $x . ',' . $y . '|' . $second . '|' . $p . $q . '|' . implode(',', $out);
}
