<?php
// construct: string concat + interpolation
// minphp: 5.6
// maxphp: 8.4
// `.` concatenation alongside simple and complex `{$...}` interpolation.

function fmt($name, $n)
{
    $arr = array('k' => 'V');
    $s = 'Hi ' . $name . ', n=' . $n;
    $t = "name=$name count={$n} key={$arr['k']}";
    return $s . '|' . $t;
}

function probe()
{
    return fmt('Sam', 3);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
