package hosts

import "testing"

// The elevated write is handed a path under the user's home, and a Windows
// home with a space in it is ordinary. PowerShell quotes nothing itself, so
// the command line has to arrive already quoted, or tamp's own argument check
// rejects the split path.
func TestTheElevatedCommandLineSurvivesASpaceInThePath(t *testing.T) {
	got := psArgumentList([]string{"hosts", "apply", `C:\Users\John Doe\.tamp\hosts.pending`})

	want := `'"hosts" "apply" "C:\Users\John Doe\.tamp\hosts.pending"'`
	if got != want {
		t.Errorf("psArgumentList = %s, want %s", got, want)
	}
}

// A single quote is what PowerShell's own literal ends on, so a home
// directory holding one must not end it early.
func TestASingleQuoteInThePathDoesNotEndThePowerShellLiteral(t *testing.T) {
	got := psArgumentList([]string{`C:\Users\O'Brien\.tamp`})

	want := `'"C:\Users\O''Brien\.tamp"'`
	if got != want {
		t.Errorf("psArgumentList = %s, want %s", got, want)
	}
}
