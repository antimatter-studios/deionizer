<?php
// construct: ??= null-coalescing assignment
// minphp: 7.4
// maxphp: 8.4
function probe() {
    $data = array('a' => 1);
    $data['a'] ??= 100;
    $data['b'] ??= 2;
    $x = null;
    $x ??= 'set';
    $y = 'keep';
    $y ??= 'no';
    return $data['a'] . ',' . $data['b'] . '|' . $x . '|' . $y;
}
echo probe();
