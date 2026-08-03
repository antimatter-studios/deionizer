<?php
// construct: null coalescing (??)
// minphp: 7.0
// maxphp: 8.4
// `??` yields the right operand when the left is null or undefined (no notice).

function lookup(array $m, $k)
{
    return $m[$k] ?? 'default';
}

function probe()
{
    $m = array('a' => 1, 'b' => null);
    $x = null;
    $x = $x ?? 'fallback';
    return lookup($m, 'a') . '|' . lookup($m, 'z') . '|' . $x;
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
