<?php
// construct: heredoc + nowdoc
// minphp: 5.6
// maxphp: 8.4
function render($name, $items) {
    $list = implode(', ', $items);
    $h = <<<TXT
Hello $name
Items: {$list}
TXT;
    $n = <<<'RAW'
Literal $name no-interp
RAW;
    return $h . '||' . $n;
}
function probe() {
    return render('Sam', array('x', 'y'));
}
echo probe();
