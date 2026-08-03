<?php
// construct: match expression
// minphp: 8.0
// maxphp: 8.4
function classify($n) {
    return match (true) {
        $n < 0 => 'neg',
        $n === 0 => 'zero',
        $n < 10 => 'small',
        default => 'big',
    };
}
function code($s) {
    return match ($s) {
        'a', 'b' => 1,
        'c' => 2,
        default => 0,
    };
}
function probe() {
    return classify(-1) . ',' . classify(0) . ',' . classify(5) . ',' . classify(99)
        . '|' . code('a') . code('b') . code('c') . code('z');
}
echo probe();
