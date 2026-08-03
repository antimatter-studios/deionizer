<?php
// construct: string interpolation forms
// minphp: 5.6
// maxphp: 8.4
class Box {
    public $y = 'Y';
    public function m() { return 'M'; }
}
function probe() {
    $x = new Box();
    $a = array('p' => 'Q', 0 => 'Z');
    $b = 'p';
    $name = 'Sam';
    $s1 = "obj={$x->y}";
    $s2 = "simpleArr=$a[$b]";
    $s3 = "idxArr=$a[0]";
    $s4 = "method={$x->m()}";
    $s5 = "prop=$x->y end";
    $s6 = "brace=${name}";
    return "$s1|$s2|$s3|$s4|$s5|$s6";
}
echo probe();
