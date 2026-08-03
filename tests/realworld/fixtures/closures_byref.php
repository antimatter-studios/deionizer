<?php
// construct: closures with use(&$ref)
// minphp: 5.6
// maxphp: 8.4
function makeCounter() {
    $n = 0;
    $inc = function () use (&$n) { $n++; return $n; };
    return $inc;
}
function probe() {
    $total = 0;
    $add = function ($x) use (&$total) { $total += $x; };
    $add(3); $add(4);
    $c = makeCounter();
    return $c() . $c() . $c() . '|' . $total;
}
echo probe();
