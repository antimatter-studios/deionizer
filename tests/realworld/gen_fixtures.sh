#!/usr/bin/env bash
#
# Generates the real-world construct corpus under tests/realworld/fixtures/.
# Each fixture follows the harness contract (tests/harness/main.go):
#   - header comments: // construct:, // minphp:, // maxphp:
#   - the whole program echoes deterministic stdout (behavioral shim), so the
#     decoded artifact is graded name-independently by what it prints.
#
# Reproducible: re-running overwrites the corpus verbatim.
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/fixtures"
mkdir -p "$DIR"

# 1. traits + conflict resolution -----------------------------------------
cat > "$DIR/traits.php" <<'EOF'
<?php
// construct: traits + conflict resolution
// minphp: 5.6
// maxphp: 8.4
trait Hello {
    public function say() { return 'hello'; }
    public function who() { return 'A'; }
}
trait World {
    public function say() { return 'world'; }
    public function who() { return 'B'; }
}
class Greeting {
    use Hello, World {
        Hello::say insteadof World;
        World::say as sayWorld;
        World::who insteadof Hello;
    }
}
function probe() {
    $g = new Greeting();
    return $g->say() . '|' . $g->sayWorld() . '|' . $g->who();
}
echo probe();
EOF

# 2. interfaces + abstract methods ----------------------------------------
cat > "$DIR/interfaces_abstract.php" <<'EOF'
<?php
// construct: interfaces + abstract methods
// minphp: 5.6
// maxphp: 8.4
interface Shape {
    const PI = 3;
    public function area();
    public function name();
}
abstract class Base implements Shape {
    abstract public function area();
    public function name() { return 'shape:' . get_class($this); }
    public function describe() { return $this->name() . '=' . $this->area(); }
}
class Square extends Base {
    private $s;
    public function __construct($s) { $this->s = $s; }
    public function area() { return $this->s * $this->s; }
}
function probe() {
    $sq = new Square(4);
    return $sq->describe() . '|' . Shape::PI . '|' . ($sq instanceof Shape ? 'yes' : 'no');
}
echo probe();
EOF

# 3. generators (yield, yield from) ---------------------------------------
cat > "$DIR/generators.php" <<'EOF'
<?php
// construct: generators (yield, yield from)
// minphp: 7.0
// maxphp: 8.4
function inner() {
    yield 'a';
    yield 'b';
}
function outer() {
    yield 'start';
    yield from inner();
    yield 'end';
}
function counter($n) {
    for ($i = 1; $i <= $n; $i++) {
        yield $i => $i * $i;
    }
}
function probe() {
    $parts = array();
    foreach (outer() as $v) { $parts[] = $v; }
    $kv = array();
    foreach (counter(3) as $k => $v) { $kv[] = "$k:$v"; }
    return implode(',', $parts) . '|' . implode(',', $kv);
}
echo probe();
EOF

# 4. closures with use(&$ref) ---------------------------------------------
cat > "$DIR/closures_byref.php" <<'EOF'
<?php
// construct: closures with use(&$ref)
// minphp: 5.6
// maxphp: 8.4
function makeCounter() {
    $n = 0;
    $inc = function () use (&$n) { $n++; return $n; };
    return $inc;
}
function probe() {
    $total = 0;
    $add = function ($x) use (&$total) { $total += $x; };
    $add(3); $add(4);
    $c = makeCounter();
    return $c() . $c() . $c() . '|' . $total;
}
echo probe();
EOF

# 5. static properties + late static binding (static::) -------------------
cat > "$DIR/late_static_binding.php" <<'EOF'
<?php
// construct: static props + late static binding
// minphp: 5.6
// maxphp: 8.4
class ModelBase {
    public static $count = 0;
    protected static function tag() { return 'base'; }
    public static function create() {
        static::$count++;
        return static::tag() . '#' . static::$count;
    }
    public static function whoSelf() { return self::tag(); }
}
class User extends ModelBase {
    public static $count = 0;
    protected static function tag() { return 'user'; }
}
function probe() {
    $a = User::create();
    $b = User::create();
    $c = ModelBase::create();
    return $a . ',' . $b . ',' . $c . '|' . User::$count . '|' . User::whoSelf();
}
echo probe();
EOF

# 6. heredoc + nowdoc ------------------------------------------------------
cat > "$DIR/heredoc_nowdoc.php" <<'EOF'
<?php
// construct: heredoc + nowdoc
// minphp: 5.6
// maxphp: 8.4
function render($name, $items) {
    $list = implode(', ', $items);
    $h = <<<TXT
Hello $name
Items: {$list}
TXT;
    $n = <<<'RAW'
Literal $name no-interp
RAW;
    return $h . '||' . $n;
}
function probe() {
    return render('Sam', array('x', 'y'));
}
echo probe();
EOF

# 7. list() / [] destructuring (incl. keyed) ------------------------------
cat > "$DIR/list_destructuring.php" <<'EOF'
<?php
// construct: list/[] destructuring (keyed, nested)
// minphp: 7.1
// maxphp: 8.4
function probe() {
    list($a, $b) = array(1, 2);
    [$c, $d] = [3, 4];
    ['x' => $x, 'y' => $y] = ['x' => 10, 'y' => 20];
    [, $second] = ['skip', 'kept'];
    [[$p], [$q]] = [[7], [8]];
    $out = array();
    foreach ([[1, 'one'], [2, 'two']] as [$num, $word]) {
        $out[] = "$num=$word";
    }
    return "$a$b$c$d|$x,$y|$second|$p$q|" . implode(',', $out);
}
echo probe();
EOF

# 8. try/catch/finally + multi-catch (A|B $e) -----------------------------
cat > "$DIR/multicatch.php" <<'EOF'
<?php
// construct: multi-catch (A|B $e) + finally
// minphp: 7.1
// maxphp: 8.4
function attempt($which) {
    $log = array();
    try {
        if ($which === 'r') { throw new RuntimeException('R'); }
        if ($which === 'l') { throw new LogicException('L'); }
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
echo probe();
EOF

# 9. references in foreach (nested) ---------------------------------------
cat > "$DIR/foreach_ref.php" <<'EOF'
<?php
// construct: references in foreach
// minphp: 5.6
// maxphp: 8.4
function probe() {
    $m = array('a' => 1, 'b' => 2, 'c' => 3);
    foreach ($m as $k => &$v) { $v = $v * 10 + strlen($k); }
    unset($v);
    $grid = array(array(1, 2), array(3, 4));
    foreach ($grid as &$row) {
        foreach ($row as &$cell) { $cell++; }
        unset($cell);
    }
    unset($row);
    $flat = array();
    foreach ($grid as $row) { $flat[] = implode('.', $row); }
    return implode(',', $m) . '|' . implode(',', $flat);
}
echo probe();
EOF

# 10. string interpolation forms ------------------------------------------
cat > "$DIR/string_interp.php" <<'EOF'
<?php
// construct: string interpolation forms
// minphp: 5.6
// maxphp: 8.4
class Box {
    public $y = 'Y';
    public function m() { return 'M'; }
}
function probe() {
    $x = new Box();
    $a = array('p' => 'Q', 0 => 'Z');
    $b = 'p';
    $name = 'Sam';
    $s1 = "obj={$x->y}";
    $s2 = "simpleArr=$a[$b]";
    $s3 = "idxArr=$a[0]";
    $s4 = "method={$x->m()}";
    $s5 = "prop=$x->y end";
    $s6 = "brace=${name}";
    return "$s1|$s2|$s3|$s4|$s5|$s6";
}
echo probe();
EOF

# 11. class constants + ::class -------------------------------------------
cat > "$DIR/class_const_classname.php" <<'EOF'
<?php
// construct: class constants + ::class
// minphp: 5.6
// maxphp: 8.4
interface HasKind { const KIND = 'iface'; }
class Widget implements HasKind {
    const VERSION = '2.0';
    const TAGS = array('a', 'b');
    public function selfRef() { return self::class; }
}
function probe() {
    $w = new Widget();
    return Widget::VERSION . '|' . implode(',', Widget::TAGS) . '|' . Widget::class . '|' . HasKind::KIND . '|' . $w->selfRef();
}
echo probe();
EOF

# 12. variadics (...$a) + spread ------------------------------------------
cat > "$DIR/variadics.php" <<'EOF'
<?php
// construct: variadics (...$a) + spread
// minphp: 5.6
// maxphp: 8.4
function sumAll(...$nums) {
    $t = 0;
    foreach ($nums as $n) { $t += $n; }
    return $t;
}
function joinPrefix($sep, ...$parts) {
    return implode($sep, $parts);
}
function probe() {
    $args = array(1, 2, 3, 4);
    $s = sumAll(...$args);
    $j = joinPrefix('-', 'a', 'b', 'c');
    return $s . '|' . $j . '|' . sumAll(10, 20);
}
echo probe();
EOF

# 13. arrow functions capturing vars --------------------------------------
cat > "$DIR/arrow_fn_capture.php" <<'EOF'
<?php
// construct: arrow fns capturing vars
// minphp: 7.4
// maxphp: 8.4
function probe() {
    $base = 100;
    $step = 5;
    $f = fn($x) => $x + $base;
    $g = fn($x) => fn($y) => $x + $y + $base;
    $nums = array_map(fn($n) => $n * $step, [1, 2, 3]);
    return $f(1) . '|' . $g(10)(20) . '|' . implode(',', $nums);
}
echo probe();
EOF

# 14. named arguments (8.0+) ----------------------------------------------
cat > "$DIR/named_args.php" <<'EOF'
<?php
// construct: named arguments
// minphp: 8.0
// maxphp: 8.4
function box($w, $h, $depth = 1, $label = 'x') {
    return "$label:$w:$h:$depth";
}
function probe() {
    $a = box(2, 3, label: 'A');
    $b = box(w: 5, h: 6, depth: 7, label: 'B');
    $c = box(1, 2, label: 'C', depth: 9);
    return "$a|$b|$c";
}
echo probe();
EOF

# 15. nullsafe operator ?-> (8.0+) ----------------------------------------
cat > "$DIR/nullsafe.php" <<'EOF'
<?php
// construct: nullsafe operator ?->
// minphp: 8.0
// maxphp: 8.4
class Addr {
    public $city;
    public function __construct($c) { $this->city = $c; }
    public function upper() { return strtoupper($this->city); }
}
class Account {
    public $addr = null;
    public function __construct($a) { $this->addr = $a; }
}
function probe() {
    $u1 = new Account(new Addr('rome'));
    $u2 = new Account(null);
    $a = $u1->addr?->upper();
    $b = $u2->addr?->upper();
    $c = $u2->addr?->city ?? 'none';
    return "$a|" . ($b === null ? 'NULL' : $b) . "|$c";
}
echo probe();
EOF

# 16. match expression (8.0+) ---------------------------------------------
cat > "$DIR/match_expr.php" <<'EOF'
<?php
// construct: match expression
// minphp: 8.0
// maxphp: 8.4
function classify($n) {
    return match (true) {
        $n < 0 => 'neg',
        $n === 0 => 'zero',
        $n < 10 => 'small',
        default => 'big',
    };
}
function code($s) {
    return match ($s) {
        'a', 'b' => 1,
        'c' => 2,
        default => 0,
    };
}
function probe() {
    return classify(-1) . ',' . classify(0) . ',' . classify(5) . ',' . classify(99)
        . '|' . code('a') . code('b') . code('c') . code('z');
}
echo probe();
EOF

# 17. __construct property promotion (8.0+) -------------------------------
cat > "$DIR/constructor_promotion.php" <<'EOF'
<?php
// construct: constructor property promotion
// minphp: 8.0
// maxphp: 8.4
class Point {
    public function __construct(
        public int $x = 0,
        public int $y = 0,
        protected string $label = 'p'
    ) {}
    public function show() { return "$this->label($this->x,$this->y)"; }
}
function probe() {
    $a = new Point(3, 4, 'A');
    $b = new Point(y: 9);
    return $a->show() . '|' . $b->show() . '|' . $a->x . ',' . $b->y;
}
echo probe();
EOF

# 18. enums (8.1+) ---------------------------------------------------------
cat > "$DIR/enums.php" <<'EOF'
<?php
// construct: enums (pure + backed)
// minphp: 8.1
// maxphp: 8.4
enum Suit: string {
    case Hearts = 'H';
    case Spades = 'S';
    public function color(): string {
        return match($this) {
            Suit::Hearts => 'red',
            Suit::Spades => 'black',
        };
    }
}
enum Status {
    case Active;
    case Closed;
    public function label(): string {
        return match($this) {
            Status::Active => 'on',
            Status::Closed => 'off',
        };
    }
}
function probe() {
    $s = Suit::Hearts;
    $t = Suit::from('S');
    $st = Status::Active;
    return $s->value . ',' . $s->color() . '|' . $t->name . ',' . $t->color()
        . '|' . $st->label() . '|' . count(Suit::cases());
}
echo probe();
EOF

# 19. first-class callable syntax (8.1+) ----------------------------------
cat > "$DIR/first_class_callable.php" <<'EOF'
<?php
// construct: first-class callable syntax
// minphp: 8.1
// maxphp: 8.4
class Calc {
    public function double($x) { return $x * 2; }
    public static function triple($x) { return $x * 3; }
}
function incr($x) { return $x + 1; }
function probe() {
    $f = incr(...);
    $c = new Calc();
    $d = $c->double(...);
    $t = Calc::triple(...);
    $s = strlen(...);
    $mapped = array_map($d, [1, 2, 3]);
    return $f(10) . '|' . $d(5) . '|' . $t(4) . '|' . $s('hello') . '|' . implode(',', $mapped);
}
echo probe();
EOF

# 20. if/else + if/elseif/else fall-through -------------------------------
# The then-block of a fall-through if/else ends in a skip-else JMP whose target
# ionCube corrupts; on Zend 4.x (8.1/8.3) the paired JMPZ target is revealed one
# past that JMP, so a target-only structurer misses it and folds the else body
# into the then-block (dropping the else). Kept in probe() itself (0-arg body
# runs at warm time — where the corruption reliably manifests) so the harness
# exercises the structurer's skip-else recovery.
cat > "$DIR/control_flow_if_else.php" <<'EOF'
<?php
// construct: if/else + if/elseif/else fall-through
// minphp: 5.6
// maxphp: 8.4
function probe() {
    $x = -3;
    if ($x > 0) {
        echo 'P';
    } else {
        echo 'N';
    }
    $y = 5;
    if ($y > 0) {
        echo 'p';
    } else {
        echo 'n';
    }
    $z = 42;
    if ($z < 0) {
        echo 'under';
    } elseif ($z < 10) {
        echo 'low';
    } elseif ($z < 100) {
        echo 'mid';
    } else {
        echo 'high';
    }
    echo '!';
    return '';
}
echo probe();
EOF

cat > "$DIR/byref_candidate_call_arg.php" <<'EOF'
<?php
// construct: by-ref-candidate array-element call arg + assigned result
// minphp: 7.4
// maxphp: 8.4
//
// Passing an array element ($vars['k']) to a method whose parameter by-ref-ness
// is unknown at compile time lowers to the 7.x/8.x by-ref-candidate send path
// (ZEND_CHECK_FUNC_ARG / ZEND_FETCH_DIM_FUNC_ARG / ZEND_SEND_FUNC_ARG) and the
// result is assigned. On production-encoded corpora ionCube renumbers the call's
// result slot so the assignment reads a phantom slot; deionizer coalesces the
// call into the assignment (renumberedCallSink). The trial encoder does not apply
// that renumbering, so this fixture cannot leak here — it pins that the construct
// still decodes and runs identically (guards the FUNC_ARG send path).
class Registry {
    public function scan($rule) { return '<' . strtoupper($rule) . '>'; }
    public function detect(array $vars) {
        $out = '';
        $conflicts = $this->scan($vars['rules']);
        $out .= $conflicts;
        $mappings = $this->scan($vars['maps']);
        $out .= '|' . $mappings;
        $summary = $this->scan($vars['sum']);
        return $out . '#' . $summary;
    }
}
function probe() {
    $r = new Registry();
    return $r->detect(array('rules' => 'alpha', 'maps' => 'beta', 'sum' => 'gamma'));
}
echo probe();
EOF

# 22a. namespaced classes — 5.6-safe basic form ---------------------------
# Proves namespace recovery on EVERY supported engine (including Zend 2.6/5.6):
# a `namespace Acme\Widgets` file with an interface, extends/implements, a static
# factory (late static binding), a FQN `new`, `instanceof`, a static property and
# a namespaced free function — all of which must render as a proper `namespace X;`
# with bare declarations and fully-qualified references. Modelled on the shape of
# late_static_binding (which recovers cleanly on 5.6) so the behavioral grade
# isolates the namespace fix from the 5.6 encrypted-scalar side-channel. Before
# the fix the class declared `class Acme_Widgets_Gadget` while references said
# `new Acme\Widgets\Gadget()`, so the decode fatally failed to find the class.
cat > "$DIR/namespaced_basic.php" <<'EOF'
<?php
// construct: namespaced classes (5.6-safe: new/static/instanceof/extends)
// minphp: 5.6
// maxphp: 8.4
namespace Acme\Widgets;

interface Named {
    public function name();
}

class WidgetBase implements Named {
    public function name() { return 'base'; }
    public function kind() { return 'widget'; }
}

class Gadget extends WidgetBase {
    public function name() { return 'gadget'; }
    public static function build() { return new Gadget(); }
}

function origin($w) {
    return get_class($w);
}

function probe() {
    $g = Gadget::build();
    $b = new WidgetBase();
    $isNamed = ($g instanceof Named) ? 'yes' : 'no';
    $isBase  = ($g instanceof WidgetBase) ? 'yes' : 'no';
    return $g->name() . ',' . $b->name() . ',' . $g->kind() . '|' . origin($g)
        . '|' . $isNamed . '|' . $isBase;
}
echo probe();
EOF

# 22b. namespaced classes + FQN refs + cross-namespace use -----------------
# The full-construct namespace case: a file in `namespace App\Domain` whose
# classes, interface, static factory, ::CONST, ::class, instanceof, catch and
# free functions must all render CONSISTENTLY — a proper `namespace X;` with bare
# declarations and fully-qualified references. Before the render fix a class
# declared `class App_Domain_Registry` while `new App\Domain\Registry()`
# referenced it, so the decode fatally failed to find the class at runtime; the
# behavioral grade catches exactly that. `use RuntimeException` exercises a
# reference from inside the namespace to the global namespace (a cross-namespace
# reference), which must qualify to `\RuntimeException`. Guarded to 7.0+: the body
# leans on class constants / string literals the Zend 2.6 (5.6) reveal encrypts at
# rest (an orthogonal limitation), which namespaced_basic covers for 5.6.
cat > "$DIR/namespaced_classes.php" <<'EOF'
<?php
// construct: namespaced classes + FQN refs + cross-namespace use
// minphp: 7.0
// maxphp: 8.4
namespace App\Domain;

use RuntimeException;

interface HasCode {
    const CODE = 'HC';
}

class Base {
    public function origin() { return 'base'; }
}

class Registry extends Base implements HasCode {
    const VERSION = '3';
    private $items = array();
    public static function create() { return new Registry(); }
    public function add($k) { $this->items[] = $k; return $this; }
    public function scan(Registry $r) { return get_class($r); }
    public function fqcn() { return Registry::class; }
    public function kind() { return Registry::CODE . ':' . Registry::VERSION; }
    public function boom() {
        try {
            throw new RuntimeException('boom');
        } catch (RuntimeException $e) {
            return 'caught:' . $e->getMessage();
        }
    }
    public function size() { return count($this->items); }
}

function make() {
    $r = Registry::create();
    $r->add('a')->add('b');
    return $r;
}

function probe() {
    $r = make();
    $isBase = ($r instanceof Base) ? 'yes' : 'no';
    return $r->origin() . '|' . $r->kind() . '|' . $r->fqcn() . '|' . $r->scan($r)
        . '|' . $r->boom() . '|' . $r->size() . '|' . $isBase;
}
echo probe();
EOF

# 23. namespaced readonly typed property + typed reference -----------------
# A `readonly Id $id` property whose declared type is a same-namespace class must
# qualify to `\App\Model\Id` under the emitted `namespace App\Model;` (the
# reflection-recovered property type is namespaced). Also covers a promoted
# readonly ctor param and an instanceof against a namespaced class.
cat > "$DIR/namespaced_typed_props.php" <<'EOF'
<?php
// construct: namespaced readonly typed property + instanceof
// minphp: 8.1
// maxphp: 8.4
namespace App\Model;

class Id {
    public function __construct(public readonly int $value) {}
    public function show() { return 'id#' . $this->value; }
}

class Node {
    public readonly Id $id;
    public function __construct(Id $id) { $this->id = $id; }
    public function label() { return $this->id->show(); }
}

function probe() {
    $n = new Node(new Id(7));
    return $n->label() . '|' . ($n->id instanceof Id ? 'Y' : 'N') . '|' . get_class($n->id);
}
echo probe();
EOF

# Dynamic member names: `$o->$m()` and `$o->$p` (op2 is a CV, not a CONST). The
# decompiler must emit the dynamic member form (`$o->{$m}()`, `$o->{$p}`), never drop
# the sigil to a bareword — the same identifier-position guard that keeps a garbled
# recovered name (e.g. a digit-leading method) `php -l`-clean.
cat > "$DIR/dynamic_member.php" <<'EOF'
<?php
// construct: dynamic method + dynamic property names
// minphp: 5.6
// maxphp: 8.4
class Widget {
    public $slot = 'S';
    public function grab() { return 'G'; }
}
function probe() {
    $o = new Widget();
    $m = 'grab';
    $p = 'slot';
    return $o->$m() . '|' . $o->$p;
}
echo probe();
EOF

# Loop structuring (FIX 2): a guard `if` immediately followed by a while whose
# ENTRY JMP is a forward jump to the condition at the loop foot — shaped exactly like
# an if/else skip-else. The structurer must recognise the loop (not fold it into a
# bogus elseif and orphan the JMPNZ latch) and hoist the assignment-in-condition, so
# there is no use-before-assign. This is the exact shape of a common assignment-in-condition
# read-loop. Scoped to 7.0+: the 5.x while-loop lift is a separate known gap.
cat > "$DIR/while_read_loop.php" <<'EOF'
<?php
// construct: guard-if then while with assignment-in-condition (read-loop)
// minphp: 7.0
// maxphp: 8.4
function probe() {
    $q = ['a', 'b', 'c', 'd'];
    $ready = false;
    if (!$ready) {
        $ready = true;
    }
    $out = '';
    while (($v = array_shift($q)) !== null) {
        $out .= $v;
    }
    return $out . ($ready ? '!' : '?');
}
echo probe();
EOF

# Nested loops with `continue` and `break` (FIX 2): the forward exits must render as
# the keyword, not be dropped (silently changing control flow) or folded into an
# elseif. while-loops keep the increment inside the body, so `continue` is sound.
cat > "$DIR/nested_loops.php" <<'EOF'
<?php
// construct: nested while loops with continue and break
// minphp: 7.0
// maxphp: 8.4
function probe() {
    $out = '';
    $i = 0;
    while ($i < 3) {
        $i++;
        $j = 0;
        while ($j < 3) {
            $j++;
            if ($j === 2) {
                continue;
            }
            if ($i === 3 && $j === 3) {
                break;
            }
            $out .= $i . $j . '-';
        }
    }
    return $out;
}
echo probe();
EOF

# Temp propagation (FIX 3): chained/nested calls produce intermediate result temps
# that ionCube renumbers; the decoder must inline each single-use temp into its one
# consumer rather than leaving a dangling `$x = $_vNN`.
cat > "$DIR/temp_chain.php" <<'EOF'
<?php
// construct: chained/nested calls producing intermediate temps
// minphp: 5.6
// maxphp: 8.4
function probe() {
    $s = '  Hello World  ';
    $r = trim(strtolower($s));
    $parts = explode(' ', $r);
    $n = count($parts);
    return $r . '|' . $n . '|' . implode('-', $parts);
}
echo probe();
EOF

echo "generated $(ls -1 "$DIR"/*.php | wc -l | tr -d ' ') fixtures in $DIR"
