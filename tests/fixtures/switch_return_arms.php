<?php
// construct: switch with return arms
// minphp: 5.6
// maxphp: 8.4
// Every arm returns directly instead of breaking, so the switch has no shared
// merge block — each grouped label jumps straight to its own RETURN, and the
// default arm returns too. Exercises the structurer's "no post-dominator"
// switch shape, distinct from the break-to-merge form in switch_fallthrough.

function monthToNr($name)
{
    switch ($name) {
        case 'jan':
        case 'january':
            return 1;
        case 'feb':
        case 'february':
            return 2;
        case 'mar':
        case 'march':
            return 3;
        default:
            return 0;
    }
}

function probe()
{
    return monthToNr('jan') . '|' . monthToNr('february') . '|'
         . monthToNr('march') . '|' . monthToNr('nope');
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
