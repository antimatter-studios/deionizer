<?php
// construct: generator return value (getReturn) + keyed yield
// minphp: 7.0
// maxphp: 8.4
function gen() {
    $sum = 0;
    foreach (array(1, 2, 3) as $i => $v) {
        $sum += $v;
        yield $i => $v * 10;
    }
    return $sum;
}
function probe() {
    $g = gen();
    $parts = array();
    foreach ($g as $k => $v) { $parts[] = "$k=$v"; }
    $ret = $g->getReturn();
    return implode(',', $parts) . '|ret=' . $ret;
}
echo probe();
