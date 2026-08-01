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
