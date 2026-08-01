<?php
// construct: keyed foreach with a nested if
// minphp: 5.6
// maxphp: 8.4
// foreach ($a as $k => $v) binds BOTH the key and the value CV, and the body
// nests a conditional that continues the loop. Exercises key-binding recovery
// (FE_FETCH with a second target) plus an if nested inside a loop body.

function checkTable(array $spec)
{
    $missing = array();
    foreach ($spec as $col => $type) {
        if ($type === '') {
            $missing[] = $col;
            continue;
        }
        if ($type === 'int' && $col !== 'id') {
            $missing[] = $col . '?';
        }
    }
    return implode(',', $missing);
}

function probe()
{
    return checkTable(array('id' => 'int', 'name' => '', 'age' => 'int', 'bio' => 'text'));
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
