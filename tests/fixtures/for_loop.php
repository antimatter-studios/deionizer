<?php
// construct: for loop
// minphp: 5.6
// maxphp: 8.4
// Classic init/condition/step counting loop accumulating a value.

function tri($n)
{
    $s = 0;
    for ($i = 1; $i <= $n; $i++) {
        $s += $i;
    }
    return $s;
}

function probe()
{
    return tri(5) . '|' . tri(10);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
