<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function probe() {
    $c = new config();
    return config::IC_UNRESOLVED_CONST . 'describe' . $c->describe();
}

class Config {
    public function describe() {
        return IC_UNRESOLVED_CONST . /* inferred */ null . IC_UNRESOLVED_CONST . /* inferred */ null . implode(',', IC_UNRESOLVED_CONST);
    }

}
