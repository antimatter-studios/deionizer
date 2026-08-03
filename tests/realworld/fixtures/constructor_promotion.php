<?php
// construct: constructor property promotion
// minphp: 8.0
// maxphp: 8.4
class Point {
    public function __construct(
        public int $x = 0,
        public int $y = 0,
        protected string $label = 'p'
    ) {}
    public function show() { return "$this->label($this->x,$this->y)"; }
}
function probe() {
    $a = new Point(3, 4, 'A');
    $b = new Point(y: 9);
    return $a->show() . '|' . $b->show() . '|' . $a->x . ',' . $b->y;
}
echo probe();
