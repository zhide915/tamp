package exitcode_test

import (
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

func TestEveryCodeKeepsItsDocumentedNumber(t *testing.T) {
	for _, tc := range []struct {
		code exitcode.Code
		want int
	}{
		{exitcode.CodeOK, 0},
		{exitcode.CodeFailed, 1},
		{exitcode.CodeUsage, 2},
		{exitcode.CodeNotFound, 3},
		{exitcode.CodeDockerUnavailable, 4},
		{exitcode.CodeNeedsYes, 5},
	} {
		if int(tc.code) != tc.want {
			t.Errorf("code = %d, want %d", tc.code, tc.want)
		}
	}
}
