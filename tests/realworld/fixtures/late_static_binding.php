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
