<?php
// construct: nested foreach
// minphp: 5.6
// maxphp: 8.4
// Inner loop over each element yielded by the outer loop.

function grid(array $rows)
{
    $out = array();
    foreach ($rows as $r => $cols) {
        foreach ($cols as $c) {
            $out[] = "$r:$c";
        }
    }
    return implode(',', $out);
}

function probe()
{
    return grid(array(array(1, 2), array(3, 4)));
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
