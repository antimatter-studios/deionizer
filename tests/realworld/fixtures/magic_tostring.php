<?php
// construct: __toString (implicit string conversion)
// minphp: 5.6
// maxphp: 8.4
class Money {
    private $cents;
    public function __construct($cents) { $this->cents = $cents; }
    public function __toString() {
        return '$' . number_format($this->cents / 100, 2);
    }
}
function probe() {
    $a = new Money(1050);
    $b = new Money(9999);
    $joined = $a . ' + ' . $b;
    return "total=$a|" . $joined . '|' . strlen((string) $b);
}
echo probe();
