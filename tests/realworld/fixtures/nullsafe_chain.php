<?php
// construct: chained nullsafe ?-> short-circuiting mid-chain
// minphp: 8.0
// maxphp: 8.4
class City {
    public $name;
    public function __construct($name) { $this->name = $name; }
    public function code() { return strtoupper(substr($this->name, 0, 3)); }
}
class Addr {
    public $city = null;
    public function __construct($c) { $this->city = $c; }
}
class User {
    public $addr = null;
    public function __construct($a) { $this->addr = $a; }
}
function probe() {
    $u1 = new User(new Addr(new City('london')));
    $u2 = new User(new Addr(null));
    $u3 = new User(null);
    $a = $u1->addr?->city?->code();
    $b = $u2->addr?->city?->code();
    $c = $u3->addr?->city?->code();
    return ($a ?? 'X') . '|' . ($b ?? 'X') . '|' . ($c ?? 'X');
}
echo probe();
