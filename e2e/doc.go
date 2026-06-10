// Package e2e holds end-to-end tests that drive the real `local-review`
// binary as a subprocess against a fake OpenAI-compatible LLM server and
// assert on its CLI output and exit code.
//
// The tests are gated behind the `e2e` build tag, so the default
// `go test ./...` does not build or run them (they shell out to `go build`
// and spawn a process — slower than a unit test, and they need `git` in
// PATH). Run them explicitly:
//
//	go test -tags e2e ./e2e/...
//
// This file (no build tag) exists only so the package always has at least
// one buildable Go file; without it, `go test ./...` would error with
// "build constraints exclude all Go files" when it reaches this directory.
package e2e
