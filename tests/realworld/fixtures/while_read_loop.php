<?php
// construct: guard-if then while with assignment-in-condition (read-loop)
// minphp: 7.0
// maxphp: 8.4
function probe() {
    $q = ['a', 'b', 'c', 'd'];
    $ready = false;
    if (!$ready) {
        $ready = true;
    }
    $out = '';
    while (($v = array_shift($q)) !== null) {
        $out .= $v;
    }
    return $out . ($ready ? '!' : '?');
}
echo probe();
