<?php
// construct: dynamic method + dynamic property names
// minphp: 5.6
// maxphp: 8.4
class Widget {
    public $slot = 'S';
    public function grab() { return 'G'; }
}
function probe() {
    $o = new Widget();
    $m = 'grab';
    $p = 'slot';
    return $o->$m() . '|' . $o->$p;
}
echo probe();
