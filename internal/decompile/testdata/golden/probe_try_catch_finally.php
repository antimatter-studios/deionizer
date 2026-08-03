<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function risky($v) {
    try {
        $log = [];
        if ($v < 0) {
            throw new InvalidArgumentException('neg');
        }
        $log[] = 'ok:' . $v;
    } catch (Exception $e) {
        $log[] = 'caught:' . $e->getMessage();
    } finally {
        $log[] = 'done';
    }
    return implode(',', $log);
}

function probe() {
    return risky(5) . '|' . risky(-1);
}
