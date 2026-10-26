package docker

import "context"

// Fake is the recording Client for tests: set the answers, run a command,
// then assert on Calls.
type Fake struct {
	// PingErr is Ping's answer.
	PingErr error

	// Calls names each method tamp called, in order.
	Calls []string
}

func (f *Fake) Ping(context.Context) error {
	f.Calls = append(f.Calls, "Ping")
	return f.PingErr
}
