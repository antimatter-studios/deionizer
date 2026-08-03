<?php
// construct: readonly typed properties (reflection-observed immutability)
// minphp: 8.1
// maxphp: 8.4
class Temperature {
    public readonly int $celsius;
    protected readonly string $scale;
    public function __construct(int $c) {
        $this->celsius = $c;
        $this->scale = 'C';
    }
    public function label(): string {
        return $this->celsius . $this->scale;
    }
}
function probe() {
    $t = new Temperature(21);
    $rp = new ReflectionProperty('Temperature', 'celsius');
    $ro = $rp->isReadOnly() ? 'ro' : 'rw';
    $sp = new ReflectionProperty('Temperature', 'scale');
    $st = $sp->getType()->getName();
    return $t->label() . '|' . $t->celsius . '|' . $ro . '|' . $st;
}
echo probe();
