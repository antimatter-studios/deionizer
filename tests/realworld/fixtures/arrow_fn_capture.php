<?php
// construct: arrow fns capturing vars
// minphp: 7.4
// maxphp: 8.4
function probe() {
    $base = 100;
    $step = 5;
    $f = fn($x) => $x + $base;
    $g = fn($x) => fn($y) => $x + $y + $base;
    $nums = array_map(fn($n) => $n * $step, [1, 2, 3]);
    return $f(1) . '|' . $g(10)(20) . '|' . implode(',', $nums);
}
echo probe();
