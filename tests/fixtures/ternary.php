<?php
// construct: ternary (full and short)
// minphp: 5.6
// maxphp: 8.4
// `a ? b : c` and the short `a ?: b` (yields a when truthy).

function pick($v)
{
    $a = $v > 0 ? 'pos' : 'nonpos';
    $b = $v ?: 'empty';
    return $a . ':' . $b;
}

function probe()
{
    return pick(5) . '|' . pick(0) . '|' . pick(-2);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
