// Package providertest stands up a fake agent CLI for tests and replays
// captured wire output through it, so a test exercises the real adapter,
// bridge or session code against what the real CLI writes, without the CLI
// installed.
//
// # The fake binary
//
// [New] returns a [Fake] whose Path is an executable named after the
// runtime's binary in its registry descriptor (claude, codex, opencode,
// agy, copilot, pi-acp). It is a symlink to the running test binary: when
// that binary starts under the fake's name, this package's init function
// takes over, replays the scripted [Run] and exits, before any test or
// TestMain runs. Importing the package is the only setup a test binary
// needs.
//
// The script travels in a hidden directory beside the symlink, found
// through argv[0], not through the environment, so the fake still works
// when the code under test replaces the child's environment. Nothing is
// written and then executed, which keeps the fake clear of ETXTBSY races,
// and it runs under go test -race and in CI with no real CLI present.
//
// Pass Path to the code under test, or call [Fake.Install] to point the
// runtime's CLI-path variable (CLAUDE_CLI_PATH, CODEX_CLI_PATH, …) and PATH
// at it ([Fake.Env] returns the same entries for code that takes an explicit
// child environment). Pass the path as given: resolving the symlink first
// runs the test binary itself instead of the fake.
//
// # Runs and steps
//
// Each invocation of the fake serves one [Run]: the first unused run whose
// [Run.When] arguments all appear in argv. [Replay] builds a run from a
// captured fixture; [Script] and [Lines] build one by hand. A run is a list
// of [Step] values: write a stdout or stderr line, send a JSON frame, wait
// for a stdin frame or any stdin line, echo stdin lines until it closes, wait
// for stdin to close, sleep, hang, or exit.
//
// Duplex transcripts (claude streaming stdio, codex app-server, ACP) pair
// each "recv" step with the frames the CLI sent in answer. A JSON-RPC
// response takes the id of the live request it answers, so a client that
// numbers its requests differently from the capture still matches. A
// notification the transcript expects but the client never sends is
// skipped; a notification the client sends that the transcript lacks is
// ignored. Any other mismatch is recorded as a fake-side error.
//
// # Calls
//
// Every invocation is recorded as a [Call]: argv, working directory,
// environment, pid, stdin lines, signals and exit code. Fake-side errors
// (no run left, unexpected input) fail the test at cleanup unless the test
// calls [Fake.ExpectErrors].
//
// # Fixtures
//
// [Fixtures] holds the corpus, one directory per runtime id (claude, codex,
// opencode, antigravity, copilot, pi); see fixtures/README.md for what each
// file is, how it was captured and which ones are synthetic. [ReadFixture],
// [FixtureLines] and [FixtureSteps] read it directly, for a test that parses
// captured output without running a fake.
//
// Runtimes come from the go-providers registry. A test that needs a runtime
// of its own registers it with registry.RegisterForTest and passes its id
// to [New].
//
// This package must not import package provider: provider's own tests use
// it, and that would be an import cycle.
package providertest
