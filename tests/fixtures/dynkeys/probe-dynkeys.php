<?php

/**
 * Dynamic-Keys source variant of probe.php.
 *
 * Identical logic to probe.php, but two methods are annotated with ionCube
 * Dynamic Key specifiers (User Guide section 4.3, ionCube Encoder 15).
 *
 * Annotation form (single-line // comment immediately above the function):
 *     // @ioncube.dk <runtime-expression> -> "<constant encoding key>" [RANDOM|BASIC]
 *
 * With RANDOM, a strong encryption method is chosen at encode time and the
 * function's bytecode stays encrypted in the file. The Loader only decrypts it
 * on first call, and only if the runtime expression evaluates to the constant
 * key string. Here the key is produced by calling dk_probe_key() with constant
 * args at the first call site, so it is computed at runtime and never stored
 * statically -- the property that defeats passive load-and-reveal.
 *
 * This is the (c) variant. Encode with the SAME flags as the default build
 * (the dynamic keys come from these annotations, not a CLI flag).
 */

namespace IonCubeOracle\Probe;

/**
 * Runtime keygen. Called by the Loader (with constant args) to reconstruct the
 * decoding key for annotated methods. Must return a string equal to the key.
 */
function dk_probe_key($salt)
{
    return 'probe-' . $salt;
}

class DeviceProbe
{
    private $threshold;
    private $labels;

    public function __construct($threshold = 10, array $labels = [])
    {
        $this->threshold = $threshold;
        $this->labels = $labels ?: ['low', 'mid', 'high'];
    }

    // @ioncube.dk dk_probe_key("vendor") -> "probe-vendor" RANDOM
    // (unqualified keygen name: the Encoder rejects namespaced/backslashed
    //  function names in the dk expression; inside this namespace it resolves
    //  to IonCubeOracle\Probe\dk_probe_key.)
    public function checkVendorCode($code, $mode = 'strict')
    {
        switch ($code) {
            case 'ALF':
            case 'ALFA':
                return 'alfa';
            case 'BRV':
            case 'BRAVO':
                return 'bravo';
            case 'CHA':
            case 'CHARLIE':
                return $mode === 'strict' ? 'charlie' : 'charlie-legacy';
            case 'DLT':
                return 'delta';
            default:
                return 'unknown';
        }
    }

    public function classify($value)
    {
        if ($value < $this->threshold) {
            $band = $this->labels[0];
        } elseif ($value < $this->threshold * 2) {
            $band = $this->labels[1];
        } else {
            $band = $this->labels[2];
        }

        $suffix = ($value % 2 === 0) ? '-even' : '-odd';

        return $band . $suffix;
    }

    public function summarize(array $items)
    {
        $out = [];
        foreach ($items as $key => $item) {
            $out[] = $key . '=' . $this->classify($item);
        }

        $i = 0;
        $acc = '';
        while ($i < count($out)) {
            $acc .= $out[$i];
            if ($i < count($out) - 1) {
                $acc .= ';';
            }
            $i++;
        }

        return $acc;
    }

    // @ioncube.dk dk_probe_key("xf") -> "probe-xf" RANDOM
    public function transform(array $data, $prefix = 'x:')
    {
        $mapper = function ($v) use ($prefix) {
            return $prefix . strtoupper($v);
        };

        $result = [];
        foreach ($data as $d) {
            $result[] = $mapper($d);
        }

        return $result;
    }
}

$p = new DeviceProbe(5, ['lo', 'mi', 'hi']);
echo $p->checkVendorCode('BRV'), "\n";
echo $p->checkVendorCode('CHA', 'loose'), "\n";
echo $p->checkVendorCode('ZZZ'), "\n";
echo $p->classify(12), "\n";
echo $p->summarize(['a' => 3, 'b' => 7, 'c' => 15]), "\n";
echo implode(',', $p->transform(['red', 'green'])), "\n";
