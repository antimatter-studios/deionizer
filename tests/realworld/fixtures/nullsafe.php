<?php
// construct: nullsafe operator ?->
// minphp: 8.0
// maxphp: 8.4
class Addr {
    public $city;
    public function __construct($c) { $this->city = $c; }
    public function upper() { return strtoupper($this->city); }
}
class Account {
    public $addr = null;
    public function __construct($a) { $this->addr = $a; }
}
function probe() {
    $u1 = new Account(new Addr('rome'));
    $u2 = new Account(null);
    $a = $u1->addr?->upper();
    $b = $u2->addr?->upper();
    $c = $u2->addr?->city ?? 'none';
    return "$a|" . ($b === null ? 'NULL' : $b) . "|$c";
}
echo probe();
