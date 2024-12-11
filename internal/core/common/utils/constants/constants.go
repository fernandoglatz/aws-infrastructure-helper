package constants

type ContextKey string

const (
	TRACE_MAP ContextKey = "TRACE-MAP"

	LOGGING_LEVEL = "LOGGING_LEVEL"
	PROFILE       = "PROFILE"
	DEV_PROFILE   = "dev"

	API_ERROR = "API_ERROR"

	MINUS_ONE = -1
	ZERO      = 0
	ONE       = 1
	FIVE      = 5
	TEN       = 10

	EMPTY    = ""
	SLASH    = "/"
	DOT      = "."
	PLUS     = "+"
	ASTERISK = "*"
	HASH     = "#"
	COLON    = ":"
	HYPHEN   = "-"
)
