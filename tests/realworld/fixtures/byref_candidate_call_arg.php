<?php
// construct: by-ref-candidate array-element call arg + assigned result
// minphp: 7.4
// maxphp: 8.4
//
// Passing an array element ($vars['k']) to a method whose parameter by-ref-ness
// is unknown at compile time lowers to the 7.x/8.x by-ref-candidate send path
// (ZEND_CHECK_FUNC_ARG / ZEND_FETCH_DIM_FUNC_ARG / ZEND_SEND_FUNC_ARG) and the
// result is assigned. On production-encoded corpora ionCube renumbers the call's
// result slot so the assignment reads a phantom slot; deionizer coalesces the
// call into the assignment (renumberedCallSink). The trial encoder does not apply
// that renumbering, so this fixture cannot leak here — it pins that the construct
// still decodes and runs identically (guards the FUNC_ARG send path).
class Registry {
    public function scan($rule) { return '<' . strtoupper($rule) . '>'; }
    public function detect(array $vars) {
        $out = '';
        $conflicts = $this->scan($vars['rules']);
        $out .= $conflicts;
        $mappings = $this->scan($vars['maps']);
        $out .= '|' . $mappings;
        $summary = $this->scan($vars['sum']);
        return $out . '#' . $summary;
    }
}
function probe() {
    $r = new Registry();
    return $r->detect(array('rules' => 'alpha', 'maps' => 'beta', 'sum' => 'gamma'));
}
echo probe();
