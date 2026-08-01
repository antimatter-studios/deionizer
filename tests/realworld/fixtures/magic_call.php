<?php
// construct: __call + __callStatic magic dispatch
// minphp: 5.6
// maxphp: 8.4
class Proxy {
    private $data = array('a' => 1, 'b' => 2);
    public function __call($name, $args) {
        return $name . ':' . implode(',', $args);
    }
    public static function __callStatic($name, $args) {
        return 'static:' . $name . '(' . count($args) . ')';
    }
}
function probe() {
    $p = new Proxy();
    $x = $p->foo(1, 2, 3);
    $y = $p->bar('z');
    $z = Proxy::make('q', 'r');
    return $x . '|' . $y . '|' . $z;
}
echo probe();
