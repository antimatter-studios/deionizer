<?php
// construct: class constants
// minphp: 5.6
// maxphp: 8.4
// Scalar and array class constants referenced via self::.

class Config
{
    const VERSION = '1.2';
    const MAX = 100;
    const LABELS = array('a', 'b');

    public function describe()
    {
        return self::VERSION . ':' . self::MAX . ':' . implode(',', self::LABELS);
    }
}

function probe()
{
    $c = new Config();
    return Config::VERSION . '|' . $c->describe();
}

// Whole-program behavioral shim (see tests/README.md): running this file
// prints the deterministic result, so a decoded artifact is graded by behavior,
// not by the (obfuscation-renamed) entry symbol.
echo probe();
