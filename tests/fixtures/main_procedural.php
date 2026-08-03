<?php
// Pure-procedural fixture: NO functions, NO classes — all logic lives in the
// file-level {main} op_array. Exercises the {main} capture + top-level render
// path (define / assignment / arithmetic / concat / array / echo / if / foreach).
define('GREETING', 'hello');
$name = 'world';
$count = 3 * 2 + 1;
$label = GREETING . ' ' . $name;
echo $label, "\n";

$items = ['a' => 1, 'b' => 2, 'c' => 3];
$total = 0;
foreach ($items as $key => $value) {
    if ($value > 1) {
        $total = $total + $value;
    }
    echo $key, '=', $value, "\n";
}

if ($total > 4) {
    echo "big total: $total\n";
} else {
    echo "small total: $total\n";
}
