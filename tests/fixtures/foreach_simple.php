<?php
// construct: foreach (value and key=>value)
// minphp: 5.6
// maxphp: 8.4
// Iterating a list by value and an associative map by key and value.

function doubler(array $xs)
{
    $out = array();
    foreach ($xs as $x) {
        $out[] = $x * 2;
    }
    return implode(',', $out);
}

function kv(array $m)
{
    $out = array();
    foreach ($m as $k => $v) {
        $out[] = "$k=$v";
    }
    return implode(',', $out);
}

function probe()
{
    return doubler(array(1, 2, 3)) . '|' . kv(array('a' => 1, 'b' => 2));
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
