<?php
// construct: by-reference params
// minphp: 5.6
// maxphp: 8.4
// `&$x` lets the callee mutate the caller's variable in place.

function bump(&$x, $by = 1)
{
    $x += $by;
}

function fill(array &$acc, $v)
{
    $acc[] = $v;
}

function probe()
{
    $n = 10;
    bump($n);
    bump($n, 5);

    $a = array();
    fill($a, 'x');
    fill($a, 'y');

    return $n . '|' . implode(',', $a);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
