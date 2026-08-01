<?php
// construct: named arguments
// minphp: 8.0
// maxphp: 8.4
function box($w, $h, $depth = 1, $label = 'x') {
    return "$label:$w:$h:$depth";
}
function probe() {
    $a = box(2, 3, label: 'A');
    $b = box(w: 5, h: 6, depth: 7, label: 'B');
    $c = box(1, 2, label: 'C', depth: 9);
    return "$a|$b|$c";
}
echo probe();
