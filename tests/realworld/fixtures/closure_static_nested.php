<?php
// construct: static closure + nested closures with capture chains
// minphp: 5.6
// maxphp: 8.4
function makeAdder($base) {
    return function ($x) use ($base) {
        $inner = function ($y) use ($base, $x) {
            return $base + $x + $y;
        };
        return $inner(1);
    };
}
function probe() {
    $sq = static function ($n) { return $n * $n; };
    $add = makeAdder(10);
    return $sq(4) . '|' . $add(5);
}
echo probe();
