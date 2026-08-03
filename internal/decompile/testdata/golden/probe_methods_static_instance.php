<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function probe() {
    $c = Counter::make(10);
    $c->inc()->inc(5);
    return $c->value() . '|' . get_class($c);
}

class Counter {
    private $n = null;

    public function __construct($start = 0) {
        $this->n = $start;
    }

    public function inc($by = 1) {
        $this->n += $by;
        return $this;
    }

    public function value() {
        return $this->n;
    }

    public static function make($start) {
        return new self($start);
    }

}
