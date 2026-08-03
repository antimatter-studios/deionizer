<?php
/**
 * deionizer analyzer  (runs INSIDE the version-matched container)
 *
 * Walks a directory tree. Each .php file is handed to a pluggable per-file
 * processor (see oracle_process()). When a file is detected as ionCube-encoded,
 * the real loader is used to define/execute it, and a best-effort reconstruction
 * is written NEXT TO the original as:  <name>.decoded-source.php
 *
 * What ".decoded-source" contains (be honest — ionCube never emits bodies):
 *   - CLASS files      -> exact interface skeleton (names, signatures, defaults,
 *                         visibility, static, constants, properties). Bodies are
 *                         placeholders because the loader hides them.
 *   - PROCEDURAL files -> recovered top-level function signatures + the captured
 *                         runtime OUTPUT (proves decode+execute), source hidden.
 *
 * At the end it prints, and writes, a manifest of every file produced.
 *
 * Modes:
 *   analyze.php <dir>          scan tree, write artifacts in place, print manifest
 *   analyze.php --one <file>   analyze one file, emit one JSON line (internal)
 */

error_reporting(E_ALL & ~E_DEPRECATED & ~E_NOTICE & ~E_WARNING);

const ARTIFACT_SUFFIX = '.decoded-source.php';

$argvv = $argv; array_shift($argvv);
if (!$argvv || $argvv[0] === '--help') {
    fwrite(STDERR, "usage: analyze.php <dir> | analyze.php --one <file>\n");
    exit($argvv ? 0 : 2);
}

/* ============================ single-file worker ============================ */
if ($argvv[0] === '--one') {
    ini_set('display_errors', '0');
    ini_set('memory_limit', '1024M');
    $file = isset($argvv[1]) ? $argvv[1] : '';
    $R = ['file'=>$file,'ok'=>false,'encoded'=>isEncoded($file),
          'classes'=>[],'functions'=>[],'output'=>'','error'=>null,'needs_loader'=>false];

    // Emit the result JSON between Record-Separator sentinels on a clean line, so
    // the parent can extract it even if the encoded file's bootstrap flushed
    // arbitrary output (e.g. an app `die(...)` with no trailing newline).
    register_shutdown_function(function () use (&$R) {
        $e = error_get_last();
        if ($e && in_array($e['type'], [E_ERROR,E_CORE_ERROR,E_COMPILE_ERROR,E_PARSE], true))
            $R['error'] = $R['error'] ?: ($e['message'].' @ '.$e['file'].':'.$e['line']);
        harvest($R);
        // drop any output buffers still open (e.g. bootstrap died before ob_get_clean)
        while (ob_get_level() > 0) {
            $buf = ob_get_clean();
            if ($R['output'] === '' && strpos($buf,'ionCube')===false && strpos($buf,'Loader')===false)
                $R['output'] = $buf;
        }
        $jf = JSON_UNESCAPED_SLASHES | JSON_PARTIAL_OUTPUT_ON_ERROR;
        if (defined('JSON_INVALID_UTF8_SUBSTITUTE')) $jf |= JSON_INVALID_UTF8_SUBSTITUTE; // 7.2+ only
        $json = json_encode($R, $jf);
        fwrite(STDOUT, "\x1e".$json."\x1e\n");
    });

    $R['_c'] = get_declared_classes();
    $R['_f'] = get_defined_functions()['user'];
    ob_start();
    // Two catches so the same handling works on PHP 5.6 (only Exception exists)
    // and on 7/8 (Error extends Throwable, not Exception). Throwable is resolved
    // lazily at throw-time, so its presence is harmless on 5.6.
    try { require $file; $R['ok'] = true; }
    catch (\Exception $t) { $R['error'] = get_class($t).': '.$t->getMessage().' @ '.$t->getFile().':'.$t->getLine(); }
    catch (\Throwable $t) { $R['error'] = get_class($t).': '.$t->getMessage().' @ '.$t->getFile().':'.$t->getLine(); }
    $out = ob_get_clean();
    if (strpos($out,'ionCube') !== false && strpos($out,'Loader') !== false) {
        $R['needs_loader'] = true; $R['error'] = 'loader missing or version/encoder mismatch';
    } else {
        $R['output'] = $out;
    }
    exit(0);
}

/* ================================ tree scan ================================ */
$dir = rtrim($argvv[0], '/');
$self = __FILE__;
$produced = [];
$rows = [];

$files = [];
$it = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($dir, FilesystemIterator::SKIP_DOTS));
foreach ($it as $f) {
    if (!$f->isFile() || strtolower($f->getExtension()) !== 'php') continue;
    if (substr($f->getFilename(), -strlen(ARTIFACT_SUFFIX)) === ARTIFACT_SUFFIX) continue; // don't reprocess our own output
    $files[] = $f->getPathname();
}
sort($files);
fwrite(STDERR, sprintf("Scanning %d PHP files under %s\n", count($files), $dir));

foreach ($files as $file) {
    // --- pluggable per-file function: returns [status, artifactText|null, meta] ---
    list($status, $artifact, $meta) = oracle_process($file, $self);

    $rel = ltrim(str_replace($dir, '', $file), '/');
    if ($artifact !== null) {
        $dest = preg_replace('/\.php$/', '', $file) . ARTIFACT_SUFFIX;
        file_put_contents($dest, $artifact);
        $produced[] = ltrim(str_replace($dir, '', $dest), '/');
    }
    $rows[] = ['file'=>$rel,'status'=>$status] + $meta;
    fwrite(STDERR, sprintf("  [%-15s] %-48s %s\n", $status, $rel, isset($meta['summary']) ? $meta['summary'] : ''));
}

// manifest + machine report at tree root
$manifestPath = "$dir/_decoded_manifest.txt";
file_put_contents($manifestPath, $produced ? implode("\n", $produced)."\n" : "");
file_put_contents("$dir/_decoded_report.json",
    json_encode(['php'=>PHP_VERSION,'loader'=>loaderVersion(),'dir'=>$dir,'produced'=>$produced,'files'=>$rows],
                JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES));

echo "\n============================================================\n";
echo "deionizer  (PHP ".PHP_VERSION.", loader ".loaderVersion().")\n";
echo "  scanned            : ".count($files)." php files\n";
echo "  artifacts produced : ".count($produced)."\n";
echo "------------------------------------------------------------\n";
echo "FILES PRODUCED:\n";
foreach ($produced as $p) echo "  $p\n";
if (!$produced) echo "  (none)\n";
echo "============================================================\n";

/* ============================ pluggable processor ========================== */
/**
 * oracle_process(): the per-file custom function the scanner applies to every
 * file. Swap/extend this to change what the tool extracts.
 * @return array{0:string,1:?string,2:array}  [status, artifactTextOrNull, meta]
 */
function oracle_process($file, $self) {
    $encoded = isEncoded($file);
    if (!$encoded) return ['PLAIN-SKIP', null, ['summary'=>'not ioncube-encoded']];

    $json = shell_exec(sprintf('php %s --one %s 2>/dev/null', escapeshellarg($self), escapeshellarg($file)));
    // extract the JSON between the last pair of RS sentinels (0x1e)
    $line = preg_match('/\x1e(.*)\x1e/s', (string)$json, $m) ? $m[1] : '';
    $d = json_decode($line, true);
    if (!is_array($d)) return ['ENCODED-FAILED', null, ['summary'=>'no parseable loader output']];

    if ($d['needs_loader'])
        return ['LOADER-MISMATCH', null, ['summary'=>'targets a different PHP version/encoder']];

    $hasDefs = $d['classes'] || $d['functions'];
    $hasOut  = trim((string)$d['output']) !== '';
    if (!$hasDefs && !$hasOut)
        return [$d['ok'] ? 'ENCODED-NOOP' : 'ENCODED-FAILED', null,
                ['summary'=>$d['ok'] ? 'loads, declares nothing' : ('error: '.$d['error'])]];

    $artifact = renderArtifact($file, $d);
    $sum = [];
    foreach ($d['classes'] as $c) $sum[] = $c['name'].'('.count($c['methods']).'m)';
    foreach ($d['functions'] as $fn) $sum[] = $fn['name'].'()';
    if ($hasOut) $sum[] = 'output:'.strlen($d['output']).'b';
    return ['RECOVERED', $artifact, ['summary'=>implode(' ', $sum),
            'classes'=>array_map(function($c){ return $c['name']; },$d['classes'])]];
}

/* ================================ rendering ================================ */
function renderArtifact($file, array $d) {
    $rel = basename($file);
    $L = ['<?php', '/**',
          ' * RECOVERED from ionCube-encoded '.$rel.' by deionizer.',
          ' * Signatures/structure are EXACT (real-loader reflection).',
          ' * Method BODIES are hidden by ionCube and are NOT recoverable — placeholders below.'];
    if (trim((string)$d['output']) !== '')
        $L[] = ' * Captured runtime OUTPUT is included at the bottom (proves decode+execute).';
    $L[] = ' */';
    $L[] = '';

    foreach ($d['classes'] as $c) $L[] = renderClass($c);
    foreach ($d['functions'] as $fn) {
        $L[] = 'function '.$fn['name'].'('.renderParams($fn['params']).') {';
        $L[] = '    /* body hidden by ionCube */';
        $L[] = '}';
    }
    if (trim((string)$d['output']) !== '') {
        $L[] = '';
        $L[] = '/* ----- captured runtime output -----';
        foreach (explode("\n", rtrim($d['output'])) as $ln) $L[] = '   '.$ln;
        $L[] = '----- end captured output ----- */';
    }
    return implode("\n", $L)."\n";
}
function renderClass(array $c) {
    $L = [];
    if ($c['obfuscation'] > 0) $L[] = '// WARNING: ~'.$c['obfuscation'].'% of names look obfuscated.';
    $decl = ($c['abstract']?'abstract ':'').$c['kind'].' '.$c['name'];
    if ($c['parent'])     $decl .= ' extends '.$c['parent'];
    if ($c['interfaces']) $decl .= ' implements '.implode(', ',$c['interfaces']);
    $L[] = $decl.' {';
    foreach ($c['constants'] as $k=>$v) $L[] = '    const '.$k.' = '.var_export($v,true).';';
    foreach ($c['properties'] as $p) $L[] = '    '.$p['vis'].($p['static']?' static':'').' $'.$p['name'].';';
    foreach ($c['methods'] as $m) {
        $sig = '    '.(isset($m['vis'])?$m['vis']:'public').(!empty($m['static'])?' static':'').' function '.$m['name'].'('.renderParams($m['params']).')';
        if (!empty($m['abstract'])) { $L[] = $sig.';'; continue; }
        $L[] = $sig.' {'; $L[] = '        /* body hidden by ionCube */'; $L[] = '    }';
    }
    $L[] = '}';
    return implode("\n", $L);
}
function renderParams(array $params) {
    return implode(', ', array_map(function($p){
        $s = (!empty($p['byref'])?'&':'').(!empty($p['variadic'])?'...':'').'$'.$p['name'];
        if (array_key_exists('default',$p)) $s .= ' = '.var_export($p['default'],true);
        elseif (!empty($p['optional']))     $s .= ' = null';
        return $s;
    }, $params));
}

/* ============================== reflection ================================= */
function harvest(array &$R) {
    if (isset($R['_h'])) return; $R['_h'] = true;
    $bc = isset($R['_c']) ? $R['_c'] : []; $bf = isset($R['_f']) ? $R['_f'] : []; unset($R['_c'],$R['_f']);
    foreach (array_diff(get_declared_classes(),$bc) as $cls) {
        try { $rc = new ReflectionClass($cls); } catch (\Exception $t) { continue; } catch (\Throwable $t) { continue; }
        if ($rc->isInternal()) continue;
        $R['classes'][] = describeClass($rc);
    }
    foreach (array_diff(get_defined_functions()['user'],$bf) as $fn) {
        try { $R['functions'][] = describeFn(new ReflectionFunction($fn)); } catch (\Exception $t) {} catch (\Throwable $t) {}
    }
}
function describeClass(ReflectionClass $rc) {
    $methods=[]; $obf=0;
    foreach ($rc->getMethods() as $m) {
        if ($m->getDeclaringClass()->getName()!==$rc->getName()) continue;
        $methods[] = describeFn($m);
        if (looksObfuscated($m->getName())) $obf++;
    }
    $props=[];
    foreach ($rc->getProperties() as $p) {
        if ($p->getDeclaringClass()->getName()!==$rc->getName()) continue;
        $props[] = ['name'=>$p->getName(),
                    'vis'=>$p->isPublic()?'public':($p->isProtected()?'protected':'private'),
                    'static'=>$p->isStatic()];
    }
    return ['name'=>$rc->getName(),
            'kind'=>$rc->isInterface()?'interface':($rc->isTrait()?'trait':'class'),
            'abstract'=>$rc->isAbstract(),
            'parent'=>($p=$rc->getParentClass())?$p->getName():null,
            'interfaces'=>array_values($rc->getInterfaceNames()),
            'constants'=>$rc->getConstants(),'properties'=>$props,'methods'=>$methods,
            'obfuscation'=>$methods?round(100*$obf/count($methods)):0];
}
function describeFn(ReflectionFunctionAbstract $m) {
    $params=[];
    foreach ($m->getParameters() as $p) {
        $x=['name'=>$p->getName()];
        if ($p->isPassedByReference()) $x['byref']=true;
        if ($p->isVariadic())          $x['variadic']=true;
        if ($p->isDefaultValueAvailable()) { try{$x['default']=$p->getDefaultValue();}catch(\Exception $t){}catch(\Throwable $t){} }
        elseif ($p->isOptional())      $x['optional']=true;
        $params[]=$x;
    }
    $i=['name'=>$m->getName(),'params'=>$params];
    if ($m instanceof ReflectionMethod) {
        $i['vis']=$m->isPublic()?'public':($m->isProtected()?'protected':'private');
        $i['static']=$m->isStatic(); $i['abstract']=$m->isAbstract();
    }
    return $i;
}
function looksObfuscated($n) { return preg_match('/^[a-z_][a-zA-Z0-9_]{2,}$/',$n)?false:true; }
function isEncoded($file) {
    $h = @file_get_contents($file,false,null,0,400);
    return $h!==false && (strpos($h,'ionCube')!==false || strpos($h,'_il_exec')!==false);
}
function loaderVersion() {
    return function_exists('ioncube_loader_version') ? ioncube_loader_version()
         : (extension_loaded('ionCube Loader') ? 'present' : 'ABSENT');
}
