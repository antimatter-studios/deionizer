<?php
// construct: __get / __set / __isset / __unset overloading
// minphp: 5.6
// maxphp: 8.4
class Bag {
    private $store = array();
    public function __get($k) { $s = $this->store; return isset($s[$k]) ? $s[$k] : 'none'; }
    public function __set($k, $v) { $this->store[$k] = $v; }
    public function __isset($k) { return isset($this->store[$k]); }
    public function __unset($k) { unset($this->store[$k]); }
}
function probe() {
    $b = new Bag();
    $b->name = 'ada';
    $b->age = 36;
    $has1 = isset($b->name) ? 'Y' : 'N';
    unset($b->age);
    $has2 = isset($b->age) ? 'Y' : 'N';
    return $b->name . '|' . $b->age . '|' . $has1 . $has2;
}
echo probe();
