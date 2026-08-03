<?php
// construct: references in foreach
// minphp: 5.6
// maxphp: 8.4
function probe() {
    $m = array('a' => 1, 'b' => 2, 'c' => 3);
    foreach ($m as $k => &$v) { $v = $v * 10 + strlen($k); }
    unset($v);
    $grid = array(array(1, 2), array(3, 4));
    foreach ($grid as &$row) {
        foreach ($row as &$cell) { $cell++; }
        unset($cell);
    }
    unset($row);
    $flat = array();
    foreach ($grid as $row) { $flat[] = implode('.', $row); }
    return implode(',', $m) . '|' . implode(',', $flat);
}
echo probe();
