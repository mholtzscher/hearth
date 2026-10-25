package exprvalue

// Fixed dialect limits from the approved automation expression contract.
const (
	sourceBytes       = 4096
	sourceNodes       = 256
	sourceDepth       = 16
	predicateDepth    = 2
	containerSize     = 128
	valueDepth        = 16
	valueNodes        = 8192
	valueBytes        = 65536
	environmentBytes  = 262144
	variableCount     = 64
	vmAllocationUnits = 32768
	// Disabled builtins parse as CallNode+IdentifierNode instead of BuiltinNode.
	// The source has already passed its stricter sourceNodes gate.
	compileParseNodes    = 2 * sourceNodes
	quoteBytes           = len(`""`)
	predicateArguments   = 2
	getArguments         = 3
	decimalExponentBound = 100000
	int64DecimalDigits   = 19
	firstPrintable       = 0x20
	nullToken            = "null"
	helperSum            = "sum"
	helperInt            = "int"
	helperMin            = "min"
	helperMax            = "max"
	helperFloat          = "float"
	helperHas            = "has"
	evaluationCode       = "expression_evaluation"
)
