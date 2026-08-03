package rawparse

// ZendLayout is the per-Zend-major description of the struct offsets, type tags
// and opcode numbers the Go parser needs to interpret the raw bytes the minimal
// C dumper memcpy'd out. This is the crux of the shim-slimming experiment: the
// version-specific knowledge that today lives (implicitly, via php.h) inside a
// per-version C shim is expressed here instead as a small typed Go table. Add
// one ZendLayout per Zend major to cover a new PHP version — no new C.
//
// Values below are verified against PHP 7.4.33 (Zend 3.4) in the ionCube 7.4
// container: sizeof(zend_op)=32, sizeof(zval)=16, IS_UNUSED=0/IS_CONST=1/
// IS_TMP_VAR=2/IS_VAR=4/IS_CV=8, and ZEND_CALL_FRAME_SLOT=5.
type ZendLayout struct {
	Name string

	// zend_op (one opline record)
	OpSize       int // sizeof(zend_op)
	OpOp1        int // znode_op op1  (byte offset within the record)
	OpOp2        int // znode_op op2
	OpResult     int // znode_op result
	OpExtended   int // extended_value (uint32)
	OpLineno     int // lineno (uint32)
	OpOpcode     int // opcode byte (ionCube-masked; XOR the keytab byte)
	OpOp1Type    int // op1_type byte
	OpOp2Type    int // op2_type byte
	OpResultType int // result_type byte
	LinenoMask   uint32

	// znode operand type tags
	TUnused, TConst, TTmp, TVar, TCV uint8

	// call frame: slot = varOffset/ZvalSize - FrameSlot
	ZvalSize  int
	FrameSlot uint32

	// zval
	ZvalTypeOff                                           int
	ZNull, ZFalse, ZTrue, ZLong, ZDouble, ZString, ZArray uint8

	// vars[]: the compiled-variable name table is an array of zend_string*,
	// so consecutive slots are PtrSize bytes apart (sizeof(zend_string*)).
	PtrSize int

	// zend_string
	StrLenOff int
	StrValOff int

	// HashTable (zend_array) + Bucket
	HTArDataOff  int
	HTNumUsedOff int
	BucketSize   int
	BucketHOff   int
	BucketKeyOff int

	// fn_flags bits
	AccStatic, AccProtected, AccPrivate, AccAbstract uint32

	// special-cased opcode numbers
	OpRECV, OpRECVInit, OpRECVVariadic                    uint8
	OpJMP, OpJMPZ, OpJMPNZ, OpJMPZNZ, OpJMPZEX, OpJMPNZEX uint8

	// opcode number -> ZEND_* mnemonic (nil-name -> ZEND_UNKNOWN_n)
	OpcodeNames map[uint8]string
}

// Zend34 is the layout for Zend 3.4 / PHP 7.4 (the only version in this proto).
var Zend34 = &ZendLayout{
	Name: "Zend 3.4 (PHP 7.4)",

	OpSize: 32, OpOp1: 8, OpOp2: 12, OpResult: 16, OpExtended: 20, OpLineno: 24,
	OpOpcode: 28, OpOp1Type: 29, OpOp2Type: 30, OpResultType: 31,
	LinenoMask: 0x1FFFFF,

	TUnused: 0, TConst: 1, TTmp: 2, TVar: 4, TCV: 8,

	ZvalSize: 16, FrameSlot: 5,

	PtrSize: 8,

	ZvalTypeOff: 8,
	ZNull:       1, ZFalse: 2, ZTrue: 3, ZLong: 4, ZDouble: 5, ZString: 6, ZArray: 7,

	StrLenOff: 16, StrValOff: 24,

	HTArDataOff: 16, HTNumUsedOff: 24, BucketSize: 32, BucketHOff: 16, BucketKeyOff: 24,

	AccStatic: 0x10, AccProtected: 0x2, AccPrivate: 0x4, AccAbstract: 0x40,

	OpRECV: 63, OpRECVInit: 64, OpRECVVariadic: 164,
	OpJMP: 42, OpJMPZ: 43, OpJMPNZ: 44, OpJMPZNZ: 45, OpJMPZEX: 46, OpJMPNZEX: 47,

	OpcodeNames: opcodeNamesZend34,
}
