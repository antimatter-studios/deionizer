<?php
// construct: match expression
// minphp: 8.0
// maxphp: 8.4
// `match` returns a value, uses strict comparison, no fall-through.

function grade($score)
{
    return match (true) {
        $score >= 90 => 'A',
        $score >= 80 => 'B',
        $score >= 70 => 'C',
        default => 'F',
    };
}

function probe()
{
    return grade(95) . '|' . grade(83) . '|' . grade(50);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
