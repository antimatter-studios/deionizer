<?php
// construct: return-by-reference method + reference bind to $this->prop
// minphp: 5.6
// maxphp: 8.4
class Registry {
    private $items = array('n' => 1, 'm' => 5);
    public function &ref($key) {
        return $this->items[$key];
    }
    public function get($key) {
        return $this->items[$key];
    }
}
function probe() {
    $r = new Registry();
    $slot = &$r->ref('n');
    $slot = 99;
    $slot2 = &$r->ref('m');
    $slot2 += 10;
    return $r->get('n') . ',' . $r->get('m');
}
echo probe();
