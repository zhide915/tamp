package hosts

import (
	"slices"
	"strings"
	"testing"
)

// The whole promise of the managed block: tamp owns what is between the
// markers and nothing else in the file.

const userFile = "127.0.0.1\tlocalhost\n" +
	"255.255.255.255\tbroadcasthost\n" +
	"# my own entry\n" +
	"10.0.0.5\tstaging.internal\n"

func TestReconcileAppendsABlockToAFileThatHasNone(t *testing.T) {
	got := Reconcile(userFile, []string{"abc.xyz.com"})

	if !strings.HasPrefix(got, userFile) {
		t.Errorf("the existing content did not survive verbatim:\n%s", got)
	}
	if !strings.HasSuffix(got, block([]string{"abc.xyz.com"}, "\n")) {
		t.Errorf("the block is not what tamp writes:\n%s", got)
	}
	if !strings.Contains(got, BeginMarker+"\n") || !strings.Contains(got, EndMarker+"\n") {
		t.Errorf("the block is not between tamp's markers:\n%s", got)
	}
	if !strings.Contains(got, "127.0.0.1  abc.xyz.com\n") {
		t.Errorf("the hostname is not pointed at loopback:\n%s", got)
	}
}

func TestReconcileRewritesOnlyInsideTheBlock(t *testing.T) {
	first := Reconcile(userFile, []string{"abc.xyz.com", "shop.example.test"})

	second := Reconcile(first, []string{"only.example.test"})

	if outside(t, second) != userFile {
		t.Errorf("content outside the block changed:\n%q", outside(t, second))
	}
	if !strings.Contains(second, "127.0.0.1  only.example.test") {
		t.Errorf("the new hostname is missing:\n%s", second)
	}
	if strings.Contains(second, "abc.xyz.com") {
		t.Errorf("a hostname tamp no longer routes survived the sync:\n%s", second)
	}
}

// A removed site must leave nothing behind — including the block itself once
// it would be empty.
func TestReconcileTakesTheWholeBlockAwayWhenNothingIsLeft(t *testing.T) {
	withBlock := Reconcile(userFile, []string{"abc.xyz.com"})

	got := Reconcile(withBlock, nil)

	if got != userFile {
		t.Errorf("removing the block did not restore the file byte for byte:\n%q", got)
	}
}

// The block is tamp's answer to "what should this file say", so a second sync
// with the same sites has nothing to write.
func TestReconcileIsIdempotent(t *testing.T) {
	once := Reconcile(userFile, []string{"a.example.test", "b.example.test"})

	if twice := Reconcile(once, []string{"b.example.test", "a.example.test"}); twice != once {
		t.Errorf("a second sync rewrote the file:\n%q\nwant:\n%q", twice, once)
	}
}

func TestReconcileLeavesAFileWithoutABlockAloneWhenThereIsNothingToWrite(t *testing.T) {
	if got := Reconcile(userFile, nil); got != userFile {
		t.Errorf("Reconcile touched a file it had nothing to add to:\n%q", got)
	}
}

// Without the terminator the begin marker would land on the end of somebody
// else's line.
func TestReconcileTerminatesALastLineThatHasNoNewline(t *testing.T) {
	got := Reconcile("10.0.0.5\tstaging.internal", []string{"abc.xyz.com"})

	// A file with no line ending to learn from gets the platform's own.
	if !strings.HasPrefix(got, "10.0.0.5\tstaging.internal"+lineEnding("")+BeginMarker) {
		t.Errorf("the block ran into the previous line:\n%q", got)
	}
}

// The one byte tamp adds outside its block: the terminator that lets a marker
// start a line. Taking the block away again leaves it there.
func TestATerminatorAddedForTheBlockOutlivesIt(t *testing.T) {
	unterminated := "10.0.0.5	staging.internal"

	got := Reconcile(Reconcile(unterminated, []string{"abc.xyz.com"}), nil)

	if got != unterminated+lineEnding("") {
		t.Errorf("Reconcile = %q, want the file plus the terminator it needed", got)
	}
}

func TestEntriesReadsBackWhatTheBlockHolds(t *testing.T) {
	file := Reconcile(userFile, []string{"b.example.test", "a.example.test"})

	got := Entries(file)

	if len(got) != 2 || got[0] != "a.example.test" || got[1] != "b.example.test" {
		t.Errorf("Entries = %v, want [a.example.test b.example.test]", got)
	}
}

// Entries answers about tamp's block only: the loopback lines the user keeps
// for their own reasons are not tamp's to report or to remove.
func TestEntriesIgnoresLoopbackLinesOutsideTheBlock(t *testing.T) {
	if got := Entries(userFile); got != nil {
		t.Errorf("Entries = %v, want none — the file has no tamp block", got)
	}
}

// Resolved reads the whole file, the user's own lines included, but never a
// comment: a commented-out line maps nothing, and words after # are no hosts.
func TestResolvedReadsEveryLoopbackLineButNoComment(t *testing.T) {
	file := "127.0.0.1  mine.example.test # my dev box\n" +
		"# 127.0.0.1  disabled.example.test\n" +
		"::1  six.example.test\n"
	file = Reconcile(file, []string{"blocked.example.test"})

	got := Resolved(file)

	want := []string{"blocked.example.test", "mine.example.test", "six.example.test"}
	if !slices.Equal(got, want) {
		t.Errorf("Resolved = %v, want %v", got, want)
	}
}

// A file with a begin marker but no end is damaged, not a block: tamp appends
// a whole one rather than guessing where the missing marker belonged.
func TestAHalfWrittenBlockIsNotTreatedAsOne(t *testing.T) {
	damaged := userFile + BeginMarker + "\n127.0.0.1  old.example.test\n"

	got := Reconcile(damaged, []string{"new.example.test"})

	if !strings.HasPrefix(got, damaged) {
		t.Errorf("tamp edited inside a block it could not identify:\n%q", got)
	}
	if !strings.HasSuffix(got, EndMarker+"\n") {
		t.Errorf("tamp did not append a complete block:\n%q", got)
	}
}

// outside is the file with tamp's block cut out — what must never change.
func outside(t *testing.T, file string) string {
	t.Helper()
	begin := strings.Index(file, BeginMarker)
	end := strings.Index(file, EndMarker)
	if begin < 0 || end < 0 {
		t.Fatalf("no tamp block in:\n%q", file)
	}
	return file[:begin] + file[end+len(EndMarker)+1:]
}

// --- what the elevated write is allowed to do ------------------------------

// The privileged half of a sync writes whatever it is handed, so it checks
// first that the hand-off can only move tamp's own block.

func TestChangesOnlyTheBlockAcceptsABlockRewrite(t *testing.T) {
	current := Reconcile(userFile, []string{"old.example.test"})
	pending := Reconcile(current, []string{"new.example.test"})

	if !ChangesOnlyTheBlock(current, pending) {
		t.Error("a plain block rewrite was refused")
	}
}

func TestChangesOnlyTheBlockAcceptsRemovingTheBlock(t *testing.T) {
	current := Reconcile(userFile, []string{"old.example.test"})

	if !ChangesOnlyTheBlock(current, userFile) {
		t.Error("taking the block away was refused")
	}
}

func TestChangesOnlyTheBlockRefusesAnEditOutsideTheBlock(t *testing.T) {
	current := Reconcile(userFile, []string{"old.example.test"})
	pending := strings.Replace(current, "10.0.0.5\tstaging.internal", "10.9.9.9\tstaging.internal", 1)

	if ChangesOnlyTheBlock(current, pending) {
		t.Error("a line outside the block was allowed through the elevated write")
	}
}

func TestChangesOnlyTheBlockRefusesAWholesaleReplacement(t *testing.T) {
	current := Reconcile(userFile, []string{"old.example.test"})

	if ChangesOnlyTheBlock(current, "127.0.0.1\tevil.example.test\n") {
		t.Error("a file with none of the original content was allowed through")
	}
}

// Windows keeps its hosts file in CRLF; a block of bare LF lines would leave
// the system's own file mixed.
func TestReconcileKeepsTheFilesOwnLineEnding(t *testing.T) {
	crlf := strings.ReplaceAll(userFile, "\n", "\r\n")

	got := Reconcile(crlf, []string{"abc.xyz.com"})

	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("the block was written with bare LF into a CRLF file:\n%q", got)
	}
	if !strings.Contains(got, "127.0.0.1  abc.xyz.com\r\n") {
		t.Errorf("the entry does not end the way the rest of the file does:\n%q", got)
	}
	if read := Entries(got); len(read) != 1 || read[0] != "abc.xyz.com" {
		t.Errorf("Entries = %v, want [abc.xyz.com]", read)
	}
	if back := Reconcile(got, nil); back != crlf {
		t.Errorf("removing the block from a CRLF file did not restore it:\n%q", back)
	}
}
