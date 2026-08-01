<?php
// construct: loop wrapping a switch
// minphp: 5.6
// maxphp: 8.4
// A while loop whose body is an entire switch with break arms. The case breaks
// target the SWITCH, not the loop, so the structurer must not mistake them for
// loop breaks — the loop's back-edge is the only jump to the top.

function checkPolarity(array $samples)
{
    $out = '';
    $i = 0;
    $count = count($samples);
    while ($i < $count) {
        switch ($samples[$i]) {
            case 'bipolar':
            case 'bi':
            case 'b':
                $out = 'BI';
                break;
            case 'unipolar':
            case 'uni':
            case 'u':
                $out = 'UNI';
                break;
        }
        $i++;
    }
    return $out;
}

function probe()
{
    return checkPolarity(array('x', 'bi')) . '|' . checkPolarity(array('uni'))
         . '|' . checkPolarity(array('none'));
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
