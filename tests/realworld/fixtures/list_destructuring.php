<?php
// construct: list/[] destructuring (keyed, nested)
// minphp: 7.1
// maxphp: 8.4
function probe() {
    list($a, $b) = array(1, 2);
    [$c, $d] = [3, 4];
    ['x' => $x, 'y' => $y] = ['x' => 10, 'y' => 20];
    [, $second] = ['skip', 'kept'];
    [[$p], [$q]] = [[7], [8]];
    $out = array();
    foreach ([[1, 'one'], [2, 'two']] as [$num, $word]) {
        $out[] = "$num=$word";
    }
    return "$a$b$c$d|$x,$y|$second|$p$q|" . implode(',', $out);
}
echo probe();
