function checkPolarity($samples) {
    $out = '';
    $i = 0;
    $count = count($samples);
    while ($i < $count) {
        switch ($samples[$i]) {
            case 'bipolar':
            case 'bi':
            case 'b':
                $out = 'BI';
                break;
            case 'unipolar':
            case 'uni':
            case 'u':
                $out = 'UNI';
                break;
        }
        $i++;
    }
    return $out;
}
