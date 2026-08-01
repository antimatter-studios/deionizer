<?php
// construct: __invoke (callable objects)
// minphp: 5.6
// maxphp: 8.4
class Multiplier {
    private $factor;
    public function __construct($factor) { $this->factor = $factor; }
    public function __invoke($x) { return $x * $this->factor; }
}
function probe() {
    $double = new Multiplier(2);
    $triple = new Multiplier(3);
    $a = $double(5);
    $b = $triple(5);
    $c = array_map($double, array(1, 2, 3));
    return $a . '|' . $b . '|' . implode(',', $c);
}
echo probe();
