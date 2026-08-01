<?php
// construct: variadic params (splat)
// minphp: 5.6
// maxphp: 8.4
// A `...$parts` tail collects extra args; call sites may also splat an array in.

function joiner($sep, ...$parts)
{
    return implode($sep, $parts);
}

function probe()
{
    $nums = array(1, 2, 3, 4);
    return joiner('-', 'a', 'b', 'c') . '|' . joiner(',', ...$nums);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
