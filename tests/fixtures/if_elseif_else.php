<?php
// construct: if / elseif / else
// minphp: 5.6
// maxphp: 8.4
// A linear cascade of conditions selecting exactly one branch.

function band($v)
{
    if ($v < 0) {
        return 'neg';
    } elseif ($v === 0) {
        return 'zero';
    } elseif ($v < 10) {
        return 'small';
    } else {
        return 'big';
    }
}

function probe()
{
    return band(-3) . '|' . band(0) . '|' . band(5) . '|' . band(100);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
