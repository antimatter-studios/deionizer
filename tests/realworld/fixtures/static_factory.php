<?php
// construct: new static() + static:: factory chains (late static binding)
// minphp: 5.6
// maxphp: 8.4
class Model {
    protected $type = 'model';
    public static function make() {
        return new static();
    }
    public function kind() {
        return $this->type;
    }
    public static function build() {
        return static::make()->kind();
    }
}
class Widget extends Model {
    protected $type = 'widget';
}
function probe() {
    $m = Model::make();
    $w = Widget::make();
    return $m->kind() . ',' . $w->kind() . '|' . Widget::build();
}
echo probe();
