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
