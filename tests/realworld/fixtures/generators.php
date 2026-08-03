<?php
// construct: generators (yield, yield from)
// minphp: 7.0
// maxphp: 8.4
function inner() {
    yield 'a';
    yield 'b';
}
function outer() {
    yield 'start';
    yield from inner();
    yield 'end';
}
function counter($n) {
    for ($i = 1; $i <= $n; $i++) {
        yield $i => $i * $i;
    }
}
function probe() {
    $parts = array();
    foreach (outer() as $v) { $parts[] = $v; }
    $kv = array();
    foreach (counter(3) as $k => $v) { $kv[] = "$k:$v"; }
    return implode(',', $parts) . '|' . implode(',', $kv);
}
echo probe();
