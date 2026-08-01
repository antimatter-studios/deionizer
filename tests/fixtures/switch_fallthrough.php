<?php
// construct: string switch with fall-through
// minphp: 5.6
// maxphp: 8.4
// Cases without a break fall into the next case; grouped labels share a body.

function classify($code)
{
    switch ($code) {
        case 'A':
        case 'B':
            $r = 'ab';
            break;
        case 'C':
            $r = 'c';
            // fall through into D
        case 'D':
            $r = isset($r) ? $r . 'd' : 'd';
            break;
        default:
            $r = 'other';
    }
    return $r;
}

function probe()
{
    return classify('A') . '|' . classify('B') . '|' . classify('C') . '|'
         . classify('D') . '|' . classify('Z');
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
