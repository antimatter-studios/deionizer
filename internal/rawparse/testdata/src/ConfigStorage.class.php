<?php
/* ConfigStorage — the other half of the rawparse equivalence corpus.
 *
 * A static config holder: one setter that populates a static array property and
 * a spread of getters that read nested keys back out. Between this and
 * TextFormat the raw blob carries enough distinct method shapes (nested array
 * fetches, defaults, ternaries, isset guards) that a byte-for-byte match against
 * deionizer_reveal_json is a real equivalence check, not a trivial one.
 */
class ConfigStorage
{
    public static $cfg = array();

    public static function setConfig($cfg)
    {
        self::$cfg = $cfg;
    }

    public static function getDirPath()
    {
        return self::$cfg['path']['log']['org'];
    }

    public static function getBufferPath()
    {
        return self::$cfg['path']['log']['backup'];
    }

    public static function getEncodedPath()
    {
        return self::$cfg['path']['log']['convert'];
    }

    public static function getSrcPath()
    {
        return self::$cfg['path']['usb'];
    }

    public static function getDebug()
    {
        return isset(self::$cfg['debug']) ? self::$cfg['debug'] : 0;
    }

    public static function getInfo($key)
    {
        if (!isset(self::$cfg['infos'][$key])) {
            return '';
        }
        return self::$cfg['infos'][$key];
    }

    public static function getRawInfo()
    {
        return self::$cfg['api']['sendTime'];
    }

    public static function isAvailable()
    {
        return self::$cfg['serial'] !== '';
    }
}
