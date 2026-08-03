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
