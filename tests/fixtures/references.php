<?php
// construct: references
// minphp: 5.6
// maxphp: 8.4
// foreach-by-reference mutation and a plain variable alias.

function normalize(array $xs)
{
    foreach ($xs as &$x) {
        $x = $x * 10;
    }
    unset($x);

    $a = 1;
    $b = &$a;
    $b = 7;

    return implode(',', $xs) . '|' . $a;
}

function probe()
{
    return normalize(array(1, 2, 3));
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
