<?php
/**
 * Batch comparator, run ONCE per (php-version x method) inside the matching
 * deionizer php image. For every fixture it:
 *
 *   1. lints the decoded artifact          (php -l)
 *   2. runs the ORIGINAL fixture           -> expected output (ground truth)
 *   3. runs the DECODED artifact           -> actual output
 *   4. reports artifact presence + behavioral equality
 *
 * Both runs go through run_probe.php, which grades by WHOLE-PROGRAM behavior and
 * never references the entry symbol name (see run_probe.php) — so an obfuscation-
 * renamed decode is judged by what it computes, not by whether a `probe()` symbol
 * survived. Each run is an isolated child `php` process (fixtures all define the
 * same entry, so they cannot coexist in one process). Output is a single JSON
 * document on stdout, consumed by the Go runner.
 *
 * Usage: php compare.php <manifest.json>
 *   manifest.json = { "driver": "<path>", "items": [ {name, original, decoded}, ... ] }
 */

error_reporting(E_ERROR | E_PARSE);
ini_set('display_errors', '0');

$manifestPath = isset($argv[1]) ? $argv[1] : '';
$manifest = json_decode(file_get_contents($manifestPath), true);
if (!is_array($manifest)) {
    fwrite(STDERR, "compare: bad manifest\n");
    exit(2);
}

$driver = $manifest['driver'];
$php = PHP_BINARY;

function run_capture($php, $driver, $file)
{
    // stdout only; stderr (parse/fatal noise) discarded.
    $cmd = escapeshellarg($php) . ' ' . escapeshellarg($driver) . ' '
         . escapeshellarg($file) . ' 2>/dev/null';
    $out = shell_exec($cmd);
    return $out === null ? '' : $out;
}

function lint($php, $file)
{
    if (!is_file($file)) {
        return array('status' => 'na', 'msg' => 'no artifact');
    }
    $cmd = escapeshellarg($php) . ' -l ' . escapeshellarg($file) . ' 2>&1';
    $out = shell_exec($cmd);
    $out = trim((string) $out);
    $ok = (strpos($out, 'No syntax errors detected') !== false);
    // Keep only the first meaningful line of an error for the report.
    $msg = '';
    if (!$ok) {
        foreach (explode("\n", $out) as $line) {
            $line = trim($line);
            if ($line !== '' && stripos($line, 'Errors parsing') === false) {
                $msg = $line;
                break;
            }
        }
    }
    return array('status' => $ok ? 'pass' : 'fail', 'msg' => $msg);
}

$results = array();
foreach ($manifest['items'] as $it) {
    $name = $it['name'];
    $orig = $it['original'];
    $dec = $it['decoded'];

    $hasArtifact = is_file($dec) && filesize($dec) > 0;

    $expected = run_capture($php, $driver, $orig);
    $actual = $hasArtifact ? run_capture($php, $driver, $dec) : '';

    $lintRes = lint($php, $dec);

    // Behavior PASS only if the original actually produced non-empty ground
    // truth AND the decoded artifact reproduced it byte-for-byte.
    $behavior = 'fail';
    if ($expected === '') {
        $behavior = 'error'; // fixture itself produced nothing under this version
    } elseif ($hasArtifact && $actual === $expected) {
        $behavior = 'pass';
    }

    $results[] = array(
        'name'     => $name,
        'artifact' => $hasArtifact,
        'lint'     => $lintRes['status'],
        'lint_msg' => $lintRes['msg'],
        'expected' => $expected,
        'actual'   => $actual,
        'behavior' => $behavior,
    );
}

echo json_encode(array('results' => $results));
