<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function probe() {
    $u1 = new Account(new Addr('rome'));
    $u2 = new Account(null);
    $a = $u1->addr?->upper();
    $b = $u2->addr?->upper();
    $c = $u2->addr?->city ?? 'none';
    return $a . '|' . (is_null($b) ? 'NULL' : $b) . ('|' . $c);
}

class Addr {
    public function __construct($c) {
        $this->city = $c;
    }

    public function upper() {
        return strtoupper($this->city);
    }

}

class Account {
    public function __construct($a) {
        $this->addr = $a;
    }

}
