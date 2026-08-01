function band($v) {
    if ($v < 0) {
        return 'neg';
    }
    if ($v === 0) {
        return 'zero';
    }
    if ($v < 10) {
        return 'small';
    }
    return 'big';
}
