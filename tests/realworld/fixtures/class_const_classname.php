<?php
// construct: class constants + ::class
// minphp: 5.6
// maxphp: 8.4
interface HasKind { const KIND = 'iface'; }
class Widget implements HasKind {
    const VERSION = '2.0';
    const TAGS = array('a', 'b');
    public function selfRef() { return self::class; }
}
function probe() {
    $w = new Widget();
    return Widget::VERSION . '|' . implode(',', Widget::TAGS) . '|' . Widget::class . '|' . HasKind::KIND . '|' . $w->selfRef();
}
echo probe();
