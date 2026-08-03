function checkTable($spec) {
    $missing = [];
    foreach ($spec as $col => $type) {
        if ($type === '') {
            $missing[] = $col;
            continue;
        }
        if ($type === 'int' && $col !== 'id') {
            $missing[] = $col . '?';
        }
    }
    return implode(',', $missing);
}
