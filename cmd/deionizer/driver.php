<?php
/* deionizer generic warm+reveal driver  (runs INSIDE the decode image)
 *
 *   php driver.php <encfile> [filter]
 *
 * Codebase-agnostic warm+reveal driver for the decode image.
 * Runs on BOTH PHP 5.6 and 7/8 (no `??`, dual Exception/Throwable catches, and
 * the autoloader never stubs core throwable types). It:
 *   1. defines common app guard constants so a module's `if(!defined(...))`
 *      bootstrap passes instead of exit()ing;
 *   2. registers a namespace-aware autoloader that eval-creates an empty stub for
 *      any missing class/interface/trait, so `require` does not fatal on
 *      unresolved dependencies;
 *   3. requires the encoded file (materialises its fns/classes via the loader);
 *   4. WARMS every new function/method with a ZERO-arg call: on 7/8 that throws
 *      ArgumentCountError from RECV and prepares the op_array WITHOUT running the
 *      body; on 5.6 (no ArgumentCountError) it runs the body with null params —
 *      either way the op_array is materialised. 0-required-param fns run their
 *      body, so they are warmed LAST;
 *   5. from a shutdown handler (survives a die()/exit() in a warmed body) reveals
 *      the prepared op_arrays via whichever reveal function the loaded decode
 *      build exposes: deionizer_reveal_json[56] (preferred; emits []opline.Method JSON)
 *      or deionizer_reveal_dump[56] (text, fed through decompile.ParseTextDump).
 *
 * Output is wrapped in Record-Separator sentinels so Go can extract it even when
 * the encoded bootstrap flushed arbitrary output first.
 */
error_reporting(0);
ini_set('display_errors', '0');
ini_set('memory_limit', '1024M');

$encfile = isset($argv[1]) ? $argv[1] : '';
$filter  = isset($argv[2]) ? $argv[2] : '';
if ($encfile === '' || !is_file($encfile)) { fwrite(STDERR, "no encfile: $encfile\n"); exit(1); }

/* --- app bootstrap guard constants (make module bootstraps pass) ------------ *
 * Encoded modules often top out with `if (!defined('SOME_GUARD')) die();` so they
 * only run inside their host app. We pre-define a generic set that covers common
 * cases. A target that checks an app-SPECIFIC guard names it at runtime via the
 * DEIONIZER_GUARD_CONSTS env var (comma-separated, `NAME` or `NAME=VALUE`), so no
 * particular application's guard name has to be baked into this tool. */
$consts = array(
    'ROOTDIR' => '/tmp', 'ABSPATH' => '/tmp', 'BASEPATH' => '/tmp',
    'APP_ROOT' => '/tmp', 'IN_APP' => true, 'ENVIRONMENT' => 'production', 'SMARTY_DIR' => '/tmp/',
);
$extra = getenv('DEIONIZER_GUARD_CONSTS');
if ($extra !== false && $extra !== '') {
    foreach (explode(',', $extra) as $pair) {
        $pair = trim($pair);
        if ($pair === '') continue;
        if (strpos($pair, '=') !== false) { list($k, $v) = explode('=', $pair, 2); $v = trim($v); }
        else { $k = $pair; $v = true; }
        $consts[trim($k)] = $v;
    }
}
foreach ($consts as $k => $v) { if (!defined($k)) define($k, $v); }
/* generic config array that static-config-holder style modules read at load */
$arrDEF = array('debug' => 1, 'serial' => '', 'path' => array('backup' => '', 'usb' => '/tmp'));

/* --- eval an empty stub for any missing class (namespace-aware) -------------- */
/* Never stub core throwable types: doing so would break catch-type resolution
 * and instanceof checks on PHP 5.6. */
$coreThrowable = array('throwable' => 1, 'exception' => 1, 'error' => 1);
/* Known engine/SPL/PSR interfaces + the interface naming conventions. A missing
 * dependency of a decoded file can be a parent CLASS (extends) or an INTERFACE
 * (implements); the autoloader sees only the name and cannot know the role. The
 * stub's kind decides which link survives: a class-stub keeps `extends` (parent
 * name recovered) but drops `implements` (5.6 silently omits an interface that
 * resolved to a class), and vice-versa. We default to a class-stub (parent
 * recovery is the higher-value signal) and switch to an interface-stub only when
 * the name is confidently an interface, so a conventionally-named external
 * interface's `implements` clause is recovered without regressing extends. */
$knownIface = array(
    'traversable'=>1,'iterator'=>1,'iteratoraggregate'=>1,'arrayaccess'=>1,
    'countable'=>1,'serializable'=>1,'jsonserializable'=>1,'stringable'=>1,
    'unitenum'=>1,'backedenum'=>1,
);
function ico_looks_like_interface($short, $knownIface) {
    $l = strtolower($short);
    if (isset($knownIface[$l])) return true;
    if (substr($l, -9) === 'interface') return true;         // FooInterface
    if (substr($l, -4) === 'able') return true;              // Countable, Serializable
    if (strlen($short) >= 2 && $short[0] === 'I' && ctype_upper($short[1])) return true; // IFoo
    return false;
}
spl_autoload_register(function ($cls) use ($coreThrowable, $knownIface) {
    if (class_exists($cls, false) || interface_exists($cls, false)) return;
    if (function_exists('trait_exists') && trait_exists($cls, false)) return;
    $p = ltrim($cls, '\\'); $ns = ''; $short = $p;
    $pos = strrpos($p, '\\');
    if ($pos !== false) { $ns = substr($p, 0, $pos); $short = substr($p, $pos + 1); }
    if (isset($coreThrowable[strtolower($short)])) return;
    $kw = ico_looks_like_interface($short, $knownIface) ? 'interface' : 'class';
    @eval(($ns !== '' ? "namespace $ns; " : '') . "$kw $short {}");
});

$fnsBefore = get_defined_functions(); $fnsBefore = $fnsBefore['user'];
$clsBefore = get_declared_classes();

/* Pick the reveal function the loaded decode build actually exposes. Names vary
 * by PHP generation: 7.x/8.x = deionizer_reveal_json (preferred) / deionizer_reveal_dump;
 * 5.6 = deionizer_reveal_json56 / deionizer_reveal_dump56. JSON is preferred when present. */
$jsonFn = null; foreach (array('deionizer_reveal_json', 'deionizer_reveal_json56') as $f) { if (function_exists($f)) { $jsonFn = $f; break; } }
$textFn = null; foreach (array('deionizer_reveal_dump', 'deionizer_reveal_dump56') as $f) { if (function_exists($f)) { $textFn = $f; break; } }
if (!$jsonFn && !$textFn) { fwrite(STDERR, "decode reveal shim not loaded (no deionizer_reveal_json* / deionizer_reveal_dump* function)\n"); }

/* --- behavioral side-channel (5.x scalar recovery) -------------------------- *
 * On Zend 2.6 scalar/non-name literals stay encrypted in the pool, so the JSON
 * reveal marks them {"t":"CONST","enc":true}. decode56's zend_execute_internal
 * hook (ic_trace_on/off) logs the REAL decrypted arguments each warmed body
 * passes to a built-in. We emit one `==== trace: Class::method ====` header per
 * warmed method and toggle the trace around its invocation, so the following
 * `IC_TRACE builtin(args)` lines are attributed to that method and Go's
 * decompile.ParseTraceFile can fill the encrypted CONST operands. */
$traceable = function_exists('ic_trace_on') && function_exists('ic_trace_off');
function ico_trace_begin($label) {
    if (!$GLOBALS['traceable']) return;
    fwrite(STDOUT, "\n==== trace: " . $label . " ====\n");
    ic_trace_on();
}
function ico_trace_end() {
    if (!$GLOBALS['traceable']) return;
    ic_trace_off();
}

/* Reveal from shutdown so a die()/exit() in a warmed body still dumps what got
 * prepared. Wrap in RS sentinels for robust extraction by Go. */
register_shutdown_function(function () use ($filter, $jsonFn, $textFn) {
    if ($jsonFn) {
        fwrite(STDOUT, "\x1eICO_JSON_BEGIN\x1e");
        $jsonFn($filter);                        // emits []opline.Method JSON on stdout
        fwrite(STDOUT, "\x1eICO_JSON_END\x1e\n");
        /* Reflection metadata Go merges onto the class-info. Two independent,
         * mutually-exclusive sources: the 5.x shim recovers class property-defaults
         * + array/expr constants (encrypted at rest on Zend 2.6); 8.1+ recovers
         * property `readonly` + declared type (carried by reflection, not the C
         * class-info). Each run is a single PHP version, so only one applies. */
        $meta = '{}';
        if (function_exists('ic_scalars_arm')) {
            $meta = ico_reflect_meta();
        } elseif (version_compare(PHP_VERSION, '8.1', '>=')) {
            $meta = ico_prop_meta();
        }
        if ($meta !== '{}' && $meta !== '') {
            fwrite(STDOUT, "\x1eICO_META_BEGIN\x1e");
            fwrite(STDOUT, $meta);
            fwrite(STDOUT, "\x1eICO_META_END\x1e\n");
        }
    } elseif ($textFn) {
        fwrite(STDOUT, "\x1eICO_TEXT_BEGIN\x1e\n");
        $textFn($filter);                        // emits the text opline listing
        fwrite(STDOUT, "\x1eICO_TEXT_END\x1e\n");
    } else {
        fwrite(STDERR, "no reveal function available — cannot reveal\n");
    }
});

/* Install the {main} capture hook OUTERMOST over ionCube, post-RINIT and before
 * the require, so a file's top-level/procedural code (a transient {main} op_array
 * in no table) is captured as its own record. Idempotent with the extension's own
 * PHP_RINIT install; a no-op on builds that predate this function. */
if (function_exists('ic_install_main_hook')) { ic_install_main_hook(); }

try { require $encfile; }
catch (Exception $e) { fwrite(STDERR, "require: " . get_class($e) . ": " . $e->getMessage() . "\n"); }
catch (Throwable $t) { fwrite(STDERR, "require: " . get_class($t) . ": " . $t->getMessage() . "\n"); }

/* Snapshot static-property defaults NOW — after the classes are declared (statics
 * at their compile-time values) but BEFORE warming, whose bodies may mutate a
 * static (`static::$count++`). The reveal prefers this snapshot for static
 * defaults so they decode as declared, not as the post-warm live value. */
if (function_exists('ic_snapshot_statics')) { ic_snapshot_statics(); }

$fnsAll = get_defined_functions(); $fnsAll = $fnsAll['user'];
$fnsNew = array_values(array_diff($fnsAll, $fnsBefore));
$clsNew = array_values(array_diff(get_declared_classes(), $clsBefore));

/* Warm order: fns with required params first (on 7/8 ArgumentCountError fires
 * before the body); 0-required-param fns last (their body runs and may die →
 * the shutdown handler still reveals what was prepared). */
$safe = array(); $risky = array();
foreach ($fnsNew as $fn) {
    try { $rf = new ReflectionFunction($fn); }
    catch (Exception $e) { continue; } catch (Throwable $t) { continue; }
    if ($rf->getNumberOfRequiredParameters() > 0) { $safe[] = $fn; } else { $risky[] = $fn; }
}
function ico_warm($fn) {
    ico_trace_begin('::' . $fn);
    try { $r = new ReflectionFunction($fn); @$r->invokeArgs(array()); }
    catch (Exception $e) {} catch (Throwable $t) {}
    ico_trace_end();
}

/* A concrete, instantiable subclass instance for an abstract class — used to warm
 * the abstract parent's OWN (non-abstract) method bodies. On Zend 2.6 a method's
 * op_array only reveals once PREPARED (invoked at least once); an abstract class
 * cannot be instantiated, so its concrete methods (a base class's shared bodies,
 * e.g. `name()`/`describe()`) were never warmed and dropped out of the reveal.
 * Invoking the PARENT's ReflectionMethod on a child instance prepares the parent's
 * own op_array (the child shares it), so it reveals under the parent. */
function ico_concrete_child($absName) {
    foreach (get_declared_classes() as $c) {
        try { $rc = new ReflectionClass($c); } catch (Exception $e) { continue; } catch (Throwable $t) { continue; }
        if ($rc->isInterface() || $rc->isAbstract() || !$rc->isUserDefined()) continue;
        if (!$rc->isSubclassOf($absName)) continue;
        try { return $rc->newInstanceWithoutConstructor(); } catch (Exception $e) {} catch (Throwable $t) {}
    }
    return null;
}
/* The instance to warm a class's own methods on. Concrete class: instantiate it
 * (no constructor). Abstract class: borrow a concrete subclass instance so the
 * shared bodies get prepared — 5.x only, since 7.x batch-decrypts every method
 * without running the body (abstract there stays null, unchanged behavior). */
function ico_warm_inst($rc) {
    if ($rc->isAbstract()) {
        if (version_compare(PHP_VERSION, '7.0', '<')) return ico_concrete_child($rc->getName());
        return null;
    }
    try { return $rc->newInstanceWithoutConstructor(); }
    catch (Exception $e) { return null; } catch (Throwable $t) { return null; }
}

/* --- reflection metadata side-channel (5.x class defaults + constants) ------- *
 * On Zend 2.6 a class's property DEFAULT values and its ARRAY/expr class CONSTANTS
 * are encrypted at rest, so the passive class-info reveal drops property defaults
 * entirely and renders an array constant as null. But the loader DECODES them in
 * place the moment reflection reads them (getConstants -> zend_update_class_constants;
 * getDefaultProperties -> the decoded default_properties_table). We read exactly
 * that loader-decoded result and emit it as a per-class map; Go fills it onto the
 * C-emitted class-info (by name), so `public $y = 'Y'` and `const TAGS = ['a','b']`
 * recover. In-bounds: the loader's own decode, read via reflection, per file.
 *
 * NON-STATIC own-property defaults + all constants only. Static defaults are left
 * to the C snapshot (warming mutates default_static_members), and non-null is the
 * proxy for "declared with a default" (a bare `$x;` reads back null and stays bare).
 * 5.x only — 7.x class-info is already cleartext, so it returns '{}' and Go skips. */
function ico_reflect_meta() {
    if (version_compare(PHP_VERSION, '7.0', '>=')) return '{}';
    $out = array();
    foreach (get_declared_classes() as $c) {
        try { $rc = new ReflectionClass($c); } catch (Exception $e) { continue; } catch (Throwable $t) { continue; }
        if (!$rc->isUserDefined() || $rc->isInternal()) continue;
        $props = array();
        $dp = @$rc->getDefaultProperties();
        if (is_array($dp)) {
            foreach ($rc->getProperties() as $rp) {
                if ($rp->isStatic()) continue;
                if ($rp->getDeclaringClass()->getName() !== $c) continue;   /* own props only */
                $n = $rp->getName();
                if (array_key_exists($n, $dp) && $dp[$n] !== null) $props[$n] = $dp[$n];
            }
        }
        $consts = @$rc->getConstants();
        /* Cast the maps to objects so an EMPTY one encodes as `{}` (a JSON object),
         * not `[]` (a JSON array) — Go unmarshals these into map[string]…, and a `[]`
         * there fails the whole decode. Constant VALUES stay arrays (a list const
         * `['a','b']` must remain `["a","b"]`, not an object). */
        $out[$c] = array('properties' => (object)$props, 'constants' => (object)(is_array($consts) ? $consts : array()));
    }
    if (empty($out)) return '{}';
    $j = json_encode($out);
    return is_string($j) ? $j : '{}';
}

/* Property `readonly` + declared type side-channel (PHP 8.1+). A readonly property
 * (ZEND_ACC_READONLY) must be typed, and neither the flag nor the type rides on the
 * C class-info reveal — but PHP reflection reports both directly (in-process, no
 * decode needed since 8.x class-info is cleartext). We emit per-class `prop_info`
 * keyed by property name; Go merges it onto the reconstructed properties. Only
 * single named types are emitted (`int`, `?string`, `Foo`); a union/intersection
 * type is left out and the renderer degrades to an untyped, non-readonly property
 * rather than emit invalid `readonly` without a type. */
function ico_prop_meta() {
    if (version_compare(PHP_VERSION, '8.1', '<')) return '{}';
    $out = array();
    foreach (get_declared_classes() as $c) {
        try { $rc = new ReflectionClass($c); } catch (Exception $e) { continue; } catch (Throwable $t) { continue; }
        if (!$rc->isUserDefined() || $rc->isInternal()) continue;
        $pinfo = array();
        foreach ($rc->getProperties() as $rp) {
            if ($rp->isStatic()) continue;
            if ($rp->getDeclaringClass()->getName() !== $c) continue;   /* own props only */
            $info = array();
            if (method_exists($rp, 'isReadOnly') && $rp->isReadOnly()) $info['readonly'] = true;
            $ty = $rp->getType();
            if ($ty instanceof ReflectionNamedType) {
                $name = $ty->getName();
                $lname = strtolower($name);
                if ($ty->allowsNull() && $lname !== 'mixed' && $lname !== 'null') $name = '?' . $name;
                $info['type'] = $name;
            }
            if (!empty($info)) $pinfo[$rp->getName()] = $info;
        }
        if (!empty($pinfo)) $out[$c] = array('prop_info' => (object)$pinfo);
    }
    if (empty($out)) return '{}';
    $j = json_encode($out);
    return is_string($j) ? $j : '{}';
}

/* --- early structural reveal (host watchdog safety net, on STDERR) ---------- *
 * Emit a reveal of everything the require already materialised BEFORE any warm
 * loop runs a user body. On 5.6 a warm RUNS the body (no ArgumentCountError), and
 * a body that never returns — e.g. a recursive filesystem copy walked with null
 * args, which descends /proc's self-referential symlinks without bound — would be
 * killed by the host's per-file watchdog before the shutdown reveal ever fired,
 * losing the whole file. This up-front reveal, captured via output buffering and
 * written to STDERR under its OWN sentinels, is a pure safety net: if the
 * container is force-removed mid-warm the host still extracts a structural decode
 * from STDERR. Critically it must NOT touch STDOUT — the shutdown reveal owns
 * STDOUT, and the C reveal (PHPWRITE) and our sentinels (php://stdout) buffer
 * independently, so a second STDOUT block could reorder under load and corrupt
 * the primary block's boundaries. Keeping the safety net on a separate stream
 * leaves the finished-run STDOUT byte-identical to a single reveal. Harmless on
 * 7/8 (warming there prepares op_arrays without running bodies). */
if ($jsonFn) {
    ob_start();
    @$jsonFn($filter);                 // captures the C reveal's PHPWRITE output
    $early = ob_get_clean();
    fwrite(STDERR, "\x1eICO_EARLY_JSON_BEGIN\x1e" . $early . "\x1eICO_EARLY_JSON_END\x1e\n");
    @fflush(STDERR);
}

foreach ($safe as $fn) { ico_warm($fn); }

foreach ($clsNew as $cls) {
    try { $rc = new ReflectionClass($cls); }
    catch (Exception $e) { continue; } catch (Throwable $t) { continue; }
    if (!$rc->isUserDefined()) continue;
    /* An enum cannot be instantiated (newInstanceWithoutConstructor throws), so a
     * non-static enum method must be warmed on one of its CASE instances or its
     * op_array is never prepared and the reveal misses it entirely. */
    $isEnum = method_exists($rc, 'isEnum') && $rc->isEnum();
    $enumInst = null;
    if ($isEnum) {
        $cs = @$cls::cases();
        if (is_array($cs) && count($cs)) $enumInst = $cs[0];
    }
    foreach ($rc->getMethods() as $m) {
        if ($m->getDeclaringClass()->getName() !== $cls) continue;
        if ($m->isAbstract()) continue;
        try {
            $m->setAccessible(true);
            ico_trace_begin($cls . '::' . $m->getName());
            if ($m->isStatic()) {
                @$m->invokeArgs(null, array());
            } else {
                $inst = $enumInst;
                if ($inst === null) {
                    try { $inst = ico_warm_inst($rc); }
                    catch (Exception $e2) { $inst = null; } catch (Throwable $t2) { $inst = null; }
                }
                if ($inst !== null) { @$m->invokeArgs($inst, array()); }
            }
            ico_trace_end();
        } catch (Exception $e) { ico_trace_end(); } catch (Throwable $t) { ico_trace_end(); }
    }
}
foreach ($risky as $fn) { ico_warm($fn); }

/* --- real-argument warming (5.x branch reach) ------------------------------ *
 * The scalar capture below only sees a CONST whose consuming opcode ACTUALLY
 * EXECUTES. Null/no-arg warming runs exactly one path through a guarded body, so
 * a literal on an untaken branch (`if($m==='strict') return 'A'; else 'B';` —
 * null takes the else) stays encrypted. We widen the reach IN-PROCESS by warming
 * each method again with several PLAUSIBLE argument shapes, so more branches run
 * and the loader decodes (and we capture) more of their scalars. Captures are
 * keyed by literal pointer and first-write-wins, so warmings only ADD — nothing a
 * prior path recovered is lost. Two seed sources, both in-bounds (read only what
 * the loader itself decoded):
 *   - generic shapes  (truthy/falsy int, (non-)empty string, array, bool) flip
 *     the common truthiness / (non-)empty guards;
 *   - mined constants — the scalar CONSTs the null pass ALREADY recovered (an
 *     equality guard's own compared-against literal, e.g. 'strict', 1, 'alpha',
 *     is captured because the compare opcode always runs) fed back as arguments,
 *     which is what drives the `===`/switch-guarded branch to actually execute.
 * Bounded: distinct seeds per method are capped; each warm is @-suppressed and
 * try/caught exactly as the null warm. 5.x only (guarded by ic_scalars_arm's
 * presence + a PHP<7 check), so 7.4/8.x are unaffected. */

/* Mine the scalar CONSTs the loader has already decoded for this file: run the
 * reveal into an output buffer and collect every {"t":"CONST","val":...} string/
 * int, keyed BY METHOD ("Class::method", "" for a function) plus a global pool.
 * Per-method keying matters: a method's OWN compared-against literal ('up' in
 * `$k==='up'`) is the seed that unlocks its guarded branch, and a flat global
 * pool with a cap would starve methods whose literals are emitted late. */
function ico_mine($jsonFn, $filter) {
    $res = array('byMethod' => array(), 'global' => array());
    if (!$jsonFn || !function_exists($jsonFn)) return $res;
    ob_start(); @$jsonFn($filter); $buf = ob_get_clean();
    if (!is_string($buf) || $buf === '') return $res;
    $gseen = array();
    $addGlobal = function ($v) use (&$res, &$gseen) {
        $sig = (is_int($v) ? 'i:' : 's:') . $v;
        if (isset($gseen[$sig])) return; $gseen[$sig] = 1;
        if (count($res['global']) < 64) $res['global'][] = $v;
    };
    $data = @json_decode($buf, true);
    if (is_array($data)) {
        foreach ($data as $rec) {
            if (!isset($rec['function'])) continue;
            $cls = isset($rec['class']) ? $rec['class'] : '';
            $key = $cls . '::' . $rec['function'];
            if (empty($rec['oplines']) || !is_array($rec['oplines'])) continue;
            $mseen = array();
            foreach ($rec['oplines'] as $op) {
                foreach (array('op1', 'op2', 'result') as $slot) {
                    if (empty($op[$slot]) || !is_array($op[$slot])) continue;
                    $o = $op[$slot];
                    if (isset($o['t']) && $o['t'] === 'CONST' && array_key_exists('val', $o)) {
                        $v = $o['val'];
                        if (!is_string($v) && !is_int($v)) continue;
                        $sig = (is_int($v) ? 'i:' : 's:') . $v;
                        if (!isset($mseen[$sig])) { $mseen[$sig] = 1; $res['byMethod'][$key][] = $v; }
                        $addGlobal($v);
                    }
                }
            }
        }
        return $res;
    }
    /* json_decode failed (unexpected) — fall back to a flat global pool by regex. */
    if (preg_match_all('/"val":("(?:[^"\\\\]|\\\\.)*"|-?[0-9]+)/', $buf, $mm)) {
        foreach ($mm[1] as $tok) { $v = json_decode($tok, true); if (is_string($v) || is_int($v)) $addGlobal($v); }
    }
    return $res;
}
/* Seed list for one method: its OWN mined constants first (they unlock its
 * ===/switch branches), then generic shapes, then a few global mined constants
 * (a literal defined elsewhere but compared here); de-duplicated and capped. */
function ico_warm_seeds($mine, $key, $capMethod, $capGlobal, $capTotal) {
    $generic = array(1, 0, -1, "1", "", true, false, array(0 => 1));
    $seeds = array(); $seen = array();
    $add = function ($v) use (&$seeds, &$seen, $capTotal) {
        if (count($seeds) >= $capTotal) return;
        $sig = gettype($v) . ':' . (is_scalar($v) ? (string)$v : 'arr');
        if (isset($seen[$sig])) return;
        $seen[$sig] = 1; $seeds[] = $v;
    };
    if (isset($mine['byMethod'][$key])) { $c = 0; foreach ($mine['byMethod'][$key] as $v) { $add($v); if (++$c >= $capMethod) break; } }
    foreach ($generic as $v) $add($v);
    $c = 0; foreach ($mine['global'] as $v) { $add($v); if (++$c >= $capGlobal) break; }
    return $seeds;
}
/* One argument for a parameter given a seed: honour an array/class type-hint so a
 * hinted param does not TypeError the whole warm; otherwise pass the seed. */
function ico_coerce_param($p, $seed) {
    if (method_exists($p, 'isArray') && @$p->isArray()) return is_array($seed) ? $seed : array($seed);
    if (@$p->getClass() !== null) return null;   /* class-hinted: no safe instance */
    return $seed;
}
/* Build the argument vectors (one per seed) for a callable's declared params. */
function ico_arg_vectors($ref, $seeds) {
    $params = $ref->getParameters(); $n = count($params);
    if ($n === 0) return array();               /* 0-param: null warm already ran it */
    $vecs = array();
    foreach ($seeds as $seed) {
        $vec = array();
        for ($i = 0; $i < $n; $i++) $vec[] = ico_coerce_param($params[$i], $seed);
        $vecs[] = $vec;
    }
    return $vecs;
}
/* Re-arm a method's CONST-consuming handlers and warm it through every arg vector
 * so each branch the vectors reach gets its scalars decoded and captured. */
function ico_realarg_method($cls, $rc, $m, $enumInst, $seeds) {
    $vecs = ico_arg_vectors($m, $seeds);
    if (!$vecs) return;
    @ic_scalars_arm($cls, $m->getName());
    $m->setAccessible(true);
    if ($m->isStatic()) {
        foreach ($vecs as $vec) { try { @$m->invokeArgs(null, $vec); } catch (Exception $e) {} catch (Throwable $t) {} }
        return;
    }
    $inst = $enumInst;
    if ($inst === null) {
        try { $inst = ico_warm_inst($rc); }
        catch (Exception $e2) { $inst = null; } catch (Throwable $t2) { $inst = null; }
    }
    if ($inst === null) return;
    foreach ($vecs as $vec) { try { @$m->invokeArgs($inst, $vec); } catch (Exception $e) {} catch (Throwable $t) {} }
}

/* --- Pass 2: transient in-place scalar capture (5.x) ------------------------ *
 * On Zend 2.6, ionCube stores scalar/non-name CONST literals as opaque words in
 * the pool and decodes each one IN PLACE, transiently, only in the private VM
 * right before the opcode handler that consumes it (no write-back). The methods
 * are prepared now, so we ARM the demasked CONST-consuming handlers of each and
 * RE-WARM it: decode's INT3 hooks snapshot the decoded operand keyed by literal
 * pointer, and deionizer_reveal_json emits the real value inline (no `enc`). This
 * recovers scalars the built-in side-channel cannot (operator operands, list
 * indices, user-call args), entirely in-process via the loader's own decode. */
if (function_exists('ic_scalars_arm')) {
    foreach ($clsNew as $cls) {
        try { $rc = new ReflectionClass($cls); }
        catch (Exception $e) { continue; } catch (Throwable $t) { continue; }
        if (!$rc->isUserDefined()) continue;
        $isEnum = method_exists($rc, 'isEnum') && $rc->isEnum();
        $enumInst = null;
        if ($isEnum) { $cs = @$cls::cases(); if (is_array($cs) && count($cs)) $enumInst = $cs[0]; }
        foreach ($rc->getMethods() as $m) {
            if ($m->getDeclaringClass()->getName() !== $cls) continue;
            if ($m->isAbstract()) continue;
            @ic_scalars_arm($cls, $m->getName());
            try {
                $m->setAccessible(true);
                if ($m->isStatic()) { @$m->invokeArgs(null, array()); }
                else {
                    $inst = $enumInst;
                    if ($inst === null) {
                        try { $inst = ico_warm_inst($rc); }
                        catch (Exception $e2) { $inst = null; } catch (Throwable $t2) { $inst = null; }
                    }
                    if ($inst !== null) { @$m->invokeArgs($inst, array()); }
                }
            } catch (Exception $e) {} catch (Throwable $t) {}
        }
    }
    foreach (array_merge($safe, $risky) as $fn) {
        @ic_scalars_arm('', $fn);
        try { $r = new ReflectionFunction($fn); @$r->invokeArgs(array()); }
        catch (Exception $e) {} catch (Throwable $t) {}
    }

    /* --- Pass 2b: real-argument warming (5.x only) -------------------------- *
     * The null pass above has now decoded every always-run scalar (crucially,
     * each guard's own compared-against literal). Mine those, then warm every
     * param-taking method/function again through generic + mined arg shapes so
     * guarded branches execute and their scalars decode too. Guarded to PHP<7 so
     * 7.4/8.x (where warming already prepares bodies without running them, and
     * scalars are cleartext anyway) are untouched. */
    if (version_compare(PHP_VERSION, '7.0', '<')) {
        $mine = ico_mine($jsonFn, $filter);
        foreach ($clsNew as $cls) {
            try { $rc = new ReflectionClass($cls); }
            catch (Exception $e) { continue; } catch (Throwable $t) { continue; }
            if (!$rc->isUserDefined()) continue;
            $isEnum = method_exists($rc, 'isEnum') && $rc->isEnum();
            $enumInst = null;
            if ($isEnum) { $cs = @$cls::cases(); if (is_array($cs) && count($cs)) $enumInst = $cs[0]; }
            foreach ($rc->getMethods() as $m) {
                if ($m->getDeclaringClass()->getName() !== $cls) continue;
                if ($m->isAbstract()) continue;
                $seeds = ico_warm_seeds($mine, $cls . '::' . $m->getName(), 10, 4, 16);
                ico_realarg_method($cls, $rc, $m, $enumInst, $seeds);
            }
        }
        foreach (array_merge($safe, $risky) as $fn) {
            try { $rf = new ReflectionFunction($fn); }
            catch (Exception $e) { continue; } catch (Throwable $t) { continue; }
            $seeds = ico_warm_seeds($mine, '::' . $fn, 10, 4, 16);
            $vecs = ico_arg_vectors($rf, $seeds);
            if (!$vecs) continue;
            @ic_scalars_arm('', $fn);
            foreach ($vecs as $vec) {
                try { @$rf->invokeArgs($vec); } catch (Exception $e) {} catch (Throwable $t) {}
            }
        }
    }
}
/* reveal fires from the shutdown handler */
