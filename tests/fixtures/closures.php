<?php
// construct: closures with use()
// minphp: 5.6
// maxphp: 8.4
// Anonymous functions that capture outer variables by value.

function makeAdder($base)
{
    return function ($x) use ($base) {
        return $base + $x;
    };
}

function probe()
{
    $add10 = makeAdder(10);
    $mul = function ($a, $b) {
        return $a * $b;
    };
    return $add10(5) . '|' . $mul(3, 4);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
