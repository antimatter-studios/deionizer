// Package opline is the SHARED CONTRACT between the C reveal shim
// (decode.c / decode56.c, which EMITS this as JSON) and the Go tooling
// (internal/decompile + the CLI, which CONSUME it).
//
// The C shim, after calling ionCube's own _su3jdmx reveal on a warmed method,
// walks the restored zend_op_array and emits one Method per encoded function/
// method, with every opcode keytab-resolved and every operand's literal/CV name
// already decoded. Go never touches PHP internals — it only reads this JSON.
//
// DO NOT change field names/JSON tags without updating both decode*.c and the
// decompiler. This file is the source of truth for the schema.
package opline

// Method is one revealed function or method: its signature plus its opline stream.
//
// A record with Kind=="class" is NOT a method but a CLASS-INFO record: the C shim
// emits one per revealed class carrying the data that lives on the zend_class_entry
// rather than in any method op_array (parent, interfaces, property and constant
// declarations). Class-info records ride in the same JSON array with an empty
// Oplines; the decompiler splits them out by Kind. Older reveal shims that do not
// emit class-info simply produce none, and the renderer falls back to method-only
// class reconstruction.
type Method struct {
	Class    string  `json:"class"`      // "" for a top-level function
	Function string  `json:"function"`   // method / function name
	File     string  `json:"file"`       // source filename the loader reported
	Static   bool    `json:"static"`     // methods only
	Vis      string  `json:"visibility"` // public|protected|private ("" for functions)
	Abstract bool    `json:"abstract,omitempty"`
	Params   []Param `json:"params"`
	NumVars  int     `json:"num_vars"` // op_array->last_var (compiled variable count)
	Oplines  []Op    `json:"oplines"`

	// Trait — set on a METHOD record when the method was flattened in from a used
	// trait (Zend copies trait methods into the using class at compile time). The
	// renderer emits `use <Trait>;` on the class and suppresses this method from the
	// class body; the method belongs to the trait's own record instead.
	Trait string `json:"trait,omitempty"`

	// Class-info fields — populated only when Kind=="class".
	//
	// Kind=="main" is neither a method nor a class: it is the file-level {main}
	// op_array (Function=="{main}") carrying a file's top-level/procedural code.
	// It rides in the same JSON array with its Oplines populated; the renderer
	// emits those as unwrapped top-level statements at file scope (renderMainBody).
	Kind       string       `json:"kind,omitempty"`       // "class"|"interface"|"trait"|"enum"|"main"; "" => a method
	Final      bool         `json:"final,omitempty"`      // class modifier
	Parent     string       `json:"parent,omitempty"`     // extends (bare name)
	Interfaces []string     `json:"interfaces,omitempty"` // implements (bare names)
	Traits     []string     `json:"traits,omitempty"`     // use T; (trait names used by this class)
	Properties []Property   `json:"properties,omitempty"` // own declared properties, in declaration order
	Constants  []ClassConst `json:"constants,omitempty"`  // own class constants
}

// Property is one declared class property (from zend_class_entry.properties_info).
type Property struct {
	Name       string      `json:"name"`             // no leading $
	Vis        string      `json:"visibility"`       // public|protected|private
	Static     bool        `json:"static,omitempty"` // static property
	HasDefault bool        `json:"has_default,omitempty"`
	Default    interface{} `json:"default,omitempty"` // resolved scalar/array when HasDefault
	Trait      string      `json:"trait,omitempty"`   // originating trait (flattened trait property)

	// ReadOnly / Type are recovered from PHP reflection (8.1+), not the C class-info:
	// a `readonly` property (ZEND_ACC_READONLY) MUST carry a type, so the two travel
	// together. The renderer emits `readonly <Type> $name` only when both are present.
	ReadOnly bool   `json:"readonly,omitempty"`
	Type     string `json:"type,omitempty"` // single named type, e.g. "int", "?string", "Foo"
}

// ClassConst is one class constant (from zend_class_entry.constants_table).
type ClassConst struct {
	Name  string      `json:"name"`
	Value interface{} `json:"value,omitempty"` // resolved scalar/array; nil when a const-expr couldn't be evaluated
	// IsCase marks an enum case (a class constant whose value is an instance of the
	// enum). The renderer emits `case Name` / `case Name = <backing>` rather than
	// `const`. Value carries the backing scalar for a backed enum, nil for a pure one.
	IsCase bool `json:"enum_case,omitempty"`
}

// Param is one declared parameter (from RECV/RECV_INIT + arg_info).
type Param struct {
	Name       string      `json:"name"`
	HasDefault bool        `json:"has_default"`
	Default    interface{} `json:"default,omitempty"` // resolved scalar/array, or null
	ByRef      bool        `json:"byref,omitempty"`
	Variadic   bool        `json:"variadic,omitempty"`
}

// Op is one zend_op (32 bytes on Zend 3.x / 48 on Zend 2.6) after reveal.
type Op struct {
	I    int     `json:"i"`      // index within the op_array
	Line int     `json:"line"`   // lineno
	Op   string  `json:"opcode"` // ZEND_* mnemonic, already keytab-resolved (opcode^key)
	Op1  Operand `json:"op1"`
	Op2  Operand `json:"op2"`
	Res  Operand `json:"result"`
	Ext  uint64  `json:"ext"` // extended_value (ZEND_ISEMPTY bit, arg counts, fetch flags…)
}

// Operand is one znode. Exactly one payload field is meaningful per T.
type Operand struct {
	T string `json:"t"` // CONST | TMP | VAR | CV | UNUSED | JMP

	// T==CONST: resolved literal. May be string/int/float/bool/nil, or for an
	// array a []interface{} / map — Encrypted=true when a 5.x scalar literal
	// could not be decrypted at rest (fill later from the behavioral side-channel).
	Val       interface{} `json:"val,omitempty"`
	Encrypted bool        `json:"enc,omitempty"`

	Var string `json:"var,omitempty"` // T==CV: the $variable name (no leading $)
	Num int    `json:"num,omitempty"` // T==TMP|VAR: temporary slot number
	Jmp int    `json:"jmp,omitempty"` // T==JMP: target opline index
}
