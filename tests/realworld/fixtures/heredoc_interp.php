<?php
// construct: heredoc with complex {$obj->prop}/{$arr['k']}/{$obj->method()} interpolation
// minphp: 5.6
// maxphp: 8.4
class Cart {
    public $items = array('apple' => 3);
    public function total() { return 42; }
}
function probe() {
    $c = new Cart();
    $user = array('name' => 'Sam', 'roles' => array('admin'));
    $h = <<<TXT
User: {$user['name']}
Apples: {$c->items['apple']}
Role: {$user['roles'][0]}
Total: {$c->total()}
TXT;
    return $h;
}
echo probe();
