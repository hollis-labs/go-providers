// Package registry is the one list of agent CLI runtimes: a [Descriptor] per
// runtime carrying everything the libraries and apps need to know about it —
// its id and aliases, binary and how to find it, the modes it can be driven in
// and what each mode can do, the permission-posture hook, its boot-dir layout,
// and what go-providers' projection does for it ([ProjectionFacts]).
//
// The vocabulary (runtime ids, modes, capabilities) is agent-contracts-leaf's
// runtimes package (D-73); this package holds the facts. The layout is not
// copied here: [Descriptor.Layout] reads package layout's table, so there is
// one list of where each runtime reads its files.
//
// # The set is closed
//
// There is no out-of-tree registration. Every descriptor is compiled into this
// package and registered by its init through an unexported function; apps look
// runtimes up ([Lookup]) and enumerate them ([All]) but cannot add one. A new
// runtime is a new runtimes.ID in agent-contracts-leaf and a new descriptor
// here. The only other way in is [RegisterForTest], which needs a test's
// cleanup hook and removes what it added when the test ends, so shared fake
// CLIs can stand in as a runtime.
//
// Copilot and Pi are ACP-only: they have no native mode, no layout rows and no
// boot dir. A runtime with a native mode always has layout rows, and the
// reverse; registration refuses a descriptor that breaks either.
package registry
