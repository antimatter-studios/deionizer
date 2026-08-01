function classify($code) {
    switch ($code) {
        case 'A':
        case 'B':
            $r = 'ab';
            break;
        case 'C':
            $r = 'c';
        case 'D':
            $r = isset($r) ? $r . 'd' : 'd';
            break;
        default:
            $r = 'other';
    }
    return $r;
}
