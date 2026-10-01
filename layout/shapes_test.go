package layout_test

import (
	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
)

// The launch shapes the tests exercise. Claude print, Codex exec, OpenCode run
// and Antigravity print are all one shape: subprocess-per-turn.
var (
	perTurn = layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}
	bare    = layout.Shape{Mode: runtimes.ModeSubprocessPerTurn, Variant: layout.VariantBare}
	pty     = layout.Shape{Mode: runtimes.ModePTY}
	jsonRPC = layout.Shape{Mode: runtimes.ModeJSONRPCStdio}
	httpSSE = layout.Shape{Mode: runtimes.ModeHTTPSSE}
)
