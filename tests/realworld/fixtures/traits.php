<?php
// construct: traits + conflict resolution
// minphp: 5.6
// maxphp: 8.4
trait Hello {
    public function say() { return 'hello'; }
    public function who() { return 'A'; }
}
trait World {
    public function say() { return 'world'; }
    public function who() { return 'B'; }
}
class Greeting {
    use Hello, World {
        Hello::say insteadof World;
        World::say as sayWorld;
        World::who insteadof Hello;
    }
}
function probe() {
    $g = new Greeting();
    return $g->say() . '|' . $g->sayWorld() . '|' . $g->who();
}
echo probe();
