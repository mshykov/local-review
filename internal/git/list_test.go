package git

import "testing"

// TestSplitNullBytes_EmptyInput covers the early-return path —
// an empty `git ls-files -z` stdout (no tracked files) should
// produce an empty slice, not a nil slice or a single empty
// string. Caller in audit.Walk relies on len() == 0 to detect
// the empty-repo case.
func TestSplitNullBytes_EmptyInput(t *testing.T) {
	got, err := splitNullBytes(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty input should produce empty slice; got %v", got)
	}
}

// TestSplitNullBytes_TrailingNullDropped covers the canonical
// `git ls-files -z` shape: tokens separated by NUL, with a
// trailing NUL at the end. The trailing NUL produces an empty
// final token which must be dropped — otherwise every TrackedFiles
// call would carry a phantom empty entry that downstream code
// would try to read as a path.
func TestSplitNullBytes_TrailingNullDropped(t *testing.T) {
	in := []byte("a\x00b\x00c\x00")
	got, err := splitNullBytes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("expected [a b c]; got %v", got)
	}
}

// TestSplitNullBytes_MissingFinalNull covers the defensive case:
// some git versions omit the final NUL on the last record. The
// scanner's at-EOF branch must yield the last token correctly.
func TestSplitNullBytes_MissingFinalNull(t *testing.T) {
	in := []byte("a\x00b\x00c") // no trailing NUL
	got, err := splitNullBytes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[2] != "c" {
		t.Errorf("expected last token preserved; got %v", got)
	}
}

// TestSplitNullBytes_PreservesPathsWithNewlines is the whole
// point of using -z mode. A path like "foo\nbar.go" must
// round-trip as one token, not split across two as it would with
// newline-separated parsing.
func TestSplitNullBytes_PreservesPathsWithNewlines(t *testing.T) {
	in := []byte("normal.go\x00foo\nbar.go\x00")
	got, err := splitNullBytes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tokens; got %d: %v", len(got), got)
	}
	if got[1] != "foo\nbar.go" {
		t.Errorf("newline-bearing path corrupted: got %q", got[1])
	}
}

// TestSplitNullBytes_MultipleConsecutiveNullsSkippedAsEmpty
// confirms that empty tokens (NUL\x00NUL\x00) don't sneak into
// the output as empty strings. `git ls-files -z` doesn't emit
// these in practice but the parser stays defensive.
func TestSplitNullBytes_MultipleConsecutiveNullsSkippedAsEmpty(t *testing.T) {
	in := []byte("a\x00\x00b\x00")
	got, err := splitNullBytes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("expected empty tokens dropped; got %v", got)
	}
}
