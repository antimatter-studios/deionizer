<?php
// construct: spread in array literal + spread into variadic call
// minphp: 7.4
// maxphp: 8.4
function sum(...$nums) {
    $t = 0;
    foreach ($nums as $n) { $t += $n; }
    return $t;
}
function probe() {
    $a = array(1, 2, 3);
    $b = array(4, 5);
    $merged = array(0, ...$a, ...$b, 6);
    $s = sum(...$a, ...$b);
    return implode(',', $merged) . '|' . $s;
}
echo probe();
