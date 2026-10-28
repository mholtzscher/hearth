package exprvalue

// Boundary limits and the native compiler/VM budgets.
const (
	sourceBytes          = 4096
	sourceNodes          = 256
	containerSize        = 128
	valueDepth           = 16
	valueNodes           = 8192
	valueBytes           = 65536
	environmentBytes     = 262144
	variableCount        = 64
	vmAllocationUnits    = 32768
	evaluationCode       = "expression_evaluation"
)
