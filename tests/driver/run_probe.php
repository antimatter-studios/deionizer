<?php
/**
 * Name-independent behavioral runner.
 *
 * Runs <target> as a WHOLE PROGRAM and prints its deterministic stdout — it
 * never invokes the entry by a hardcoded symbol name, so an obfuscation-renamed
 * decode is still graded by the behavior it produces:
 *
 *   1. Include the file. If it self-emits at top level (every fixture carries an
 *      `echo probe();` shim, and any decode that reconstructs {main} does too),
 *      that captured output IS the result — no function name is consulted.
 *   2. Otherwise the file only DEFINED code (our decoder currently drops the
 *      encoded {main}), so invoke the reconstructed entry STRUCTURALLY: the
 *      last-declared user function callable with zero required arguments. The
 *      encoder scrambles the name `probe` under obfuscation; selecting the entry
 *      by shape rather than by name is what makes renamed decodes gradable.
 *
 * Errors (a parse error in a lossy/obfuscated decode, a fatal in a wrong body)
 * are suppressed and yield empty stdout, so the harness scores that cell FAIL —
 * never a fabricated pass. Kept PHP 5.6-compatible: no Throwable catch, no `??`.
 *
 * Usage: php run_probe.php <target.php>
 *
 * The harness compares this stdout for the ORIGINAL fixture (ground truth)
 * against the DECODED artifact.
 */

error_reporting(0);
ini_set('display_errors', '0');

$target = isset($argv[1]) ? $argv[1] : '';
if ($target === '' || !is_file($target)) {
    fwrite(STDERR, "run_probe: missing target\n");
    exit(2);
}

$before = get_defined_functions();
$before = $before['user'];

// A parse error in the target is fatal (empty stdout); on 7+ it is an uncaught
// ParseError, on 5.x an E_PARSE — same net effect. We deliberately do NOT catch
// (Throwable does not exist on 5.6), so the driver itself stays 5.6-loadable.
ob_start();
require $target;
$top = ob_get_clean();
if (trim($top) !== '') {
    echo $top;              // whole-program output — the name-independent path
    return;
}

// The file only declared code; find and run the reconstructed entry point by
// shape (zero required args), not by name.
$after = get_defined_functions();
$new = array_values(array_diff($after['user'], $before));
$entry = null;
foreach ($new as $fn) {
    $r = new ReflectionFunction($fn);
    if ($r->getNumberOfRequiredParameters() === 0) {
        $entry = $fn;       // keep the LAST zero-argument function (the entry)
    }
}
if ($entry !== null) {
    $v = $entry();
    echo is_string($v) ? $v : var_export($v, true);
}
