<?php
// construct: functions with default params
// minphp: 5.6
// maxphp: 8.4
// Optional args fall back to their declared defaults when omitted.

function greet($name, $greeting = 'Hello', $punct = '!')
{
    return "$greeting, $name$punct";
}

function probe()
{
    return greet('World') . '|' . greet('Sam', 'Hi') . '|' . greet('A', 'Yo', '?');
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
