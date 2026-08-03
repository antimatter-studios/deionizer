<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function incr($x) {
    return $x + 1;
}

function probe() {
    $f = incr(...);
    $c = new Calc();
    $d = $c->double(...);
    $t = Calc::triple(...);
    $s = strlen(...);
    $mapped = array_map($d, [1, 2, 3]);
    return $f(10) . '|' . $d(5) . '|' . $t(4) . '|' . $s('hello') . '|' . implode(',', $mapped);
}

class Calc {
    public function double($x) {
        return $x * 2;
    }

    public static function triple($x) {
        return $x * 3;
    }

}
