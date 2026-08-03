<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function probe() {
    $c = counter::make(/* inferred */ null);
    $c->inc()->inc(/* inferred */ null);
    return $c->value() . /* inferred */ null . get_class($c);
}

class Counter {
    public function __construct($start = null) {
        $this->{'/* decompiler: property name unresolved */'} = $start;
    }

    public function inc($by = null) {
        $this->{'/* decompiler: property name unresolved */'} += $by;
        return $this;
    }

    public function value() {
        return $this->{'/* decompiler: property name unresolved */'};
    }

    public static function make($start) {
        return new self($start);
    }

}
