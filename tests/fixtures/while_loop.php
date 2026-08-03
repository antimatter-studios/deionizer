<?php
// construct: while loop
// minphp: 5.6
// maxphp: 8.4
// Pre-tested loop that may execute zero times.

function countdown($n)
{
    $out = array();
    while ($n > 0) {
        $out[] = $n;
        $n--;
    }
    return implode(',', $out);
}

function probe()
{
    return countdown(4) . '|' . countdown(0);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
