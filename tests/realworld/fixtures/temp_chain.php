<?php
// construct: chained/nested calls producing intermediate temps
// minphp: 5.6
// maxphp: 8.4
function probe() {
    $s = '  Hello World  ';
    $r = trim(strtolower($s));
    $parts = explode(' ', $r);
    $n = count($parts);
    return $r . '|' . $n . '|' . implode('-', $parts);
}
echo probe();
