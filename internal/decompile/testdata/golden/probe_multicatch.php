<?php
// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).
// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered
// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.

function attempt($which) {
    try {
        $log = [];
        if ($which === 'r') {
            throw new RuntimeException('R');
        }
        if ($which === 'l') {
            throw new LogicException('L');
        }
        $log[] = 'ok';
    } catch (RuntimeException | LogicException $e) {
        $log[] = 'caught:' . get_class($e) . ':' . $e->getMessage();
    } finally {
        $log[] = 'fin';
    }
    return implode(',', $log);
}

function probe() {
    return attempt('r') . '|' . attempt('l') . '|' . attempt('x');
}
