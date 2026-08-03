<?php
// construct: nested / associative arrays
// minphp: 5.6
// maxphp: 8.4
// Associative + nested literals, append, and deep index access.

function build()
{
    $a = array(
        'name' => 'probe',
        'nums' => array(1, 2, 3),
        'meta' => array('deep' => array('x' => 9)),
    );
    $a['nums'][] = 4;
    return $a['name'] . '|' . implode(',', $a['nums']) . '|' . $a['meta']['deep']['x'];
}

function probe()
{
    return build();
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
