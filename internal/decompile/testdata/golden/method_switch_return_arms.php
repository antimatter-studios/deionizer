function monthToNr($name) {
    switch ($name) {
        case 'jan':
        case 'january':
            return 1;
        case 'feb':
        case 'february':
            return 2;
        case 'mar':
        case 'march':
            return 3;
        default:
            return 0;
    }
}
