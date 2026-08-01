<?php
// construct: arrow functions
// minphp: 7.4
// maxphp: 8.4
// `fn() =>` auto-captures outer scope by value; single-expression body.

function probe()
{
    $factor = 3;
    $f = fn($x) => $x * $factor;
    $nums = array_map(fn($n) => $n + 1, array(1, 2, 3));
    return $f(4) . '|' . implode(',', $nums);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
