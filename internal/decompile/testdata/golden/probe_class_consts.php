<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function probe() {
    $c = new Config();
    return '1.2|' . $c->describe();
}

class Config {
    const VERSION = '1.2';
    const MAX = 100;
    const LABELS = ['a', 'b'];

    public function describe() {
        return '1.2:100:' . implode(',', ['a', 'b']);
    }

}
