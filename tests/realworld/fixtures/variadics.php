<?php
// construct: variadics (...$a) + spread
// minphp: 5.6
// maxphp: 8.4
function sumAll(...$nums) {
    $t = 0;
    foreach ($nums as $n) { $t += $n; }
    return $t;
}
function joinPrefix($sep, ...$parts) {
    return implode($sep, $parts);
}
function probe() {
    $args = array(1, 2, 3, 4);
    $s = sumAll(...$args);
    $j = joinPrefix('-', 'a', 'b', 'c');
    return $s . '|' . $j . '|' . sumAll(10, 20);
}
echo probe();
