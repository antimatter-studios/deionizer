<?php
// construct: namespaced readonly typed property + instanceof
// minphp: 8.1
// maxphp: 8.4
namespace App\Model;

class Id {
    public function __construct(public readonly int $value) {}
    public function show() { return 'id#' . $this->value; }
}

class Node {
    public readonly Id $id;
    public function __construct(Id $id) { $this->id = $id; }
    public function label() { return $this->id->show(); }
}

function probe() {
    $n = new Node(new Id(7));
    return $n->label() . '|' . ($n->id instanceof Id ? 'Y' : 'N') . '|' . get_class($n->id);
}
echo probe();
