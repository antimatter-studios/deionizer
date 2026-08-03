<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function checkPolarity($samples) {
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

function probe() {
    return checkpolarity(['x', 'bi']) . '|' . checkpolarity(['uni']) . '|' . checkpolarity(['none']);
}
