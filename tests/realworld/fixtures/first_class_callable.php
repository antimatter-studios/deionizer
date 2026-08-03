<?php
// construct: first-class callable syntax
// minphp: 8.1
// maxphp: 8.4
class Calc {
    public function double($x) { return $x * 2; }
    public static function triple($x) { return $x * 3; }
}
function incr($x) { return $x + 1; }
function probe() {
    $f = incr(...);
    $c = new Calc();
    $d = $c->double(...);
    $t = Calc::triple(...);
    $s = strlen(...);
    $mapped = array_map($d, [1, 2, 3]);
    return $f(10) . '|' . $d(5) . '|' . $t(4) . '|' . $s('hello') . '|' . implode(',', $mapped);
}
echo probe();
