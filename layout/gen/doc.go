// Command gen renders the layout table into docs/LAYOUT.md (human table) and
// layout/layout.json (for non-Go readers such as Cairn layouts and
// agent-launcher). It is run by "go generate ./..." from package layout.
//
//	go run ./layout/gen          write both files
//	go run ./layout/gen -check   exit 1 when either file is stale
//
// The module root is found by walking up from the working directory to go.mod;
// -root overrides it.
package main
