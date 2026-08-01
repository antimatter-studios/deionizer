<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

// decompiler: top-level ({main}) statements
define('GREETING', 'hello');
$name = 'world';
$count = 7;
$label = GREETING . ' ' . $name;
echo $label;
echo "\n";
$items = ['a' => 1, 'b' => 2, 'c' => 3];
$total = 0;
foreach ($items as $key => $value) {
    if (1 < $value) {
        $total = $total + $value;
    }
    echo $key;
    echo '=';
    echo $value;
    echo "\n";
}
if (4 < $total) {
    echo 'big total: ' . $total . "\n";
} else { // decompiler: else extent inferred (ionCube obfuscates the merge JMP)
    echo 'small total: ' . $total . "\n";
}
