<?php
// construct: do-while loop
// minphp: 5.6
// maxphp: 8.4
// Post-tested loop whose body always runs at least once.

function atLeastOnce($n)
{
    $out = array();
    $i = 0;
    do {
        $out[] = $i;
        $i++;
    } while ($i < $n);
    return implode(',', $out);
}

function probe()
{
    return atLeastOnce(3) . '|' . atLeastOnce(0);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
