<?php
/* TextFormat — half of the rawparse equivalence corpus.
 *
 * Shapes this class exists to exercise (see rawparse_test.go):
 *   - a public static property read back through self::$conf[...]  (the op0
 *     ZEND_FETCH_STATIC_PROP_R the spot-checks assert)
 *   - a method with a required CV param AND a defaulted empty-array param,
 *     which compiles to RECV + RECV_INIT with an array-literal default
 *   - a spread of static methods so the raw blob holds a meaningful method table
 */
class TextFormat
{
    public static $conf = array();

    public static function isCli()
    {
        return self::$conf['isCLI'];
    }

    public static function printInfo($strArr, $arr = array())
    {
        if (count($arr) > 0) {
            return $strArr . ':' . implode(',', $arr);
        }
        return $strArr;
    }

    public static function printDebug($msg)
    {
        if (empty(self::$conf['debug'])) {
            return '';
        }
        return '[dbg] ' . $msg;
    }

    public static function printR($arr)
    {
        $out = array();
        foreach ($arr as $k => $v) {
            $out[] = $k . '=' . $v;
        }
        return implode(';', $out);
    }

    public static function shortName($path)
    {
        $pos = strrpos($path, '/');
        if ($pos === false) {
            return $path;
        }
        return substr($path, $pos + 1);
    }

    public static function testConfig()
    {
        return isset(self::$conf['root']) ? 'ok' : 'unset';
    }

    public static function printError($msg = 'error')
    {
        return '[err] ' . $msg;
    }

    public static function join($sep, $parts)
    {
        return implode($sep, $parts);
    }
}
