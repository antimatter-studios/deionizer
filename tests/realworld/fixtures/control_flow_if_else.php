<?php
// construct: if/else + if/elseif/else fall-through
// minphp: 5.6
// maxphp: 8.4
function probe() {
    $x = -3;
    if ($x > 0) {
        echo 'P';
    } else {
        echo 'N';
    }
    $y = 5;
    if ($y > 0) {
        echo 'p';
    } else {
        echo 'n';
    }
    $z = 42;
    if ($z < 0) {
        echo 'under';
    } elseif ($z < 10) {
        echo 'low';
    } elseif ($z < 100) {
        echo 'mid';
    } else {
        echo 'high';
    }
    echo '!';
    return '';
}
echo probe();
