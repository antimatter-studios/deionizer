<?php
// construct: interfaces + abstract methods
// minphp: 5.6
// maxphp: 8.4
interface Shape {
    const PI = 3;
    public function area();
    public function name();
}
abstract class Base implements Shape {
    abstract public function area();
    public function name() { return 'shape:' . get_class($this); }
    public function describe() { return $this->name() . '=' . $this->area(); }
}
class Square extends Base {
    private $s;
    public function __construct($s) { $this->s = $s; }
    public function area() { return $this->s * $this->s; }
}
function probe() {
    $sq = new Square(4);
    return $sq->describe() . '|' . Shape::PI . '|' . ($sq instanceof Shape ? 'yes' : 'no');
}
echo probe();
