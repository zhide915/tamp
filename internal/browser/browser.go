// Package browser hands a URL to the machine's default browser. It is a
// process boundary like the hosts file, not a service tamp models: tamp only
// asks the operating system to open something and reports what it said.
package browser

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Open asks the operating system to open url in the default browser. It
// returns once the launcher has started, not once a window appears — no
// operating system reports that back, and a launcher that falls through to a
// terminal browser would never return at all.
//
// ctx is deliberately unused: it ends when tamp exits, and a browser started
// under it would be killed with the command that opened it.
func Open(_ context.Context, url string) error {
	name, args := command(url)
	if err := exec.Command(name, args...).Start(); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot open %s: %v", url, err),
			"open the URL above in your browser yourself")
	}
	return nil
}

// command is each operating system's way of opening a URL. Windows goes
// through rundll32 rather than 'cmd /c start', which reads a leading quoted
// argument as a window title and would need the URL quoted twice.
func command(url string) (string, []string) {
	switch runtime.GOOS {
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		return "open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}
