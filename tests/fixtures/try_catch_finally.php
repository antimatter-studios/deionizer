<?php
// construct: try / catch / finally
// minphp: 5.6
// maxphp: 8.4
// Exception thrown, caught by type, with a finally block that always runs.

function risky($v)
{
    $log = array();
    try {
        if ($v < 0) {
            throw new \InvalidArgumentException('neg');
        }
        $log[] = "ok:$v";
    } catch (\Exception $e) {
        $log[] = 'caught:' . $e->getMessage();
    } finally {
        $log[] = 'done';
    }
    return implode(',', $log);
}

function probe()
{
    return risky(5) . '|' . risky(-1);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
