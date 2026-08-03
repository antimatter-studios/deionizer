<?php
// construct: keyed + nested list() destructuring in foreach
// minphp: 7.1
// maxphp: 8.4
function probe() {
    $rows = array(
        array('id' => 1, 'name' => 'ada', 'tags' => array('x', 'y')),
        array('id' => 2, 'name' => 'linus', 'tags' => array('z', 'w')),
    );
    $out = array();
    foreach ($rows as ['id' => $id, 'name' => $name, 'tags' => [$first, $second]]) {
        $out[] = "$id:$name:$first$second";
    }
    return implode('|', $out);
}
echo probe();
