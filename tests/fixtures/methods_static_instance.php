<?php
// construct: static + instance methods
// minphp: 5.6
// maxphp: 8.4
// Static factory, instance methods, fluent `return $this` chaining.

class Counter
{
    private $n;

    public function __construct($start = 0)
    {
        $this->n = $start;
    }

    public function inc($by = 1)
    {
        $this->n += $by;
        return $this;
    }

    public function value()
    {
        return $this->n;
    }

    public static function make($start)
    {
        return new self($start);
    }
}

function probe()
{
    $c = Counter::make(10);
    $c->inc()->inc(5);
    return $c->value() . '|' . get_class($c);
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
