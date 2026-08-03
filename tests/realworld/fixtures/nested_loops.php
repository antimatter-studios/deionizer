<?php
// construct: nested while loops with continue and break
// minphp: 7.0
// maxphp: 8.4
function probe() {
    $out = '';
    $i = 0;
    while ($i < 3) {
        $i++;
        $j = 0;
        while ($j < 3) {
            $j++;
            if ($j === 2) {
                continue;
            }
            if ($i === 3 && $j === 3) {
                break;
            }
            $out .= $i . $j . '-';
        }
    }
    return $out;
}
echo probe();
