<?php
// construct: Closure::bind / bindTo / ->call with $this capture
// minphp: 7.0
// maxphp: 8.4
class Box {
    private $secret = 42;
}
function probe() {
    $reader = function () { return $this->secret; };
    $a = Closure::bind($reader, new Box(), Box::class);
    $b = $reader->bindTo(new Box(), Box::class);
    $c = $reader->call(new Box());
    return $a() . ',' . $b() . ',' . $c;
}
echo probe();
