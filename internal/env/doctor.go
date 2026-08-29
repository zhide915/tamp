package env

import (
	"github.com/zhide915/tamp/internal/doctor"
	"github.com/zhide915/tamp/internal/hosts"
)

// Diagnostics is what doctor cannot read for itself: the machine's registry
// and the state of tamp's block in the hosts file. A file that refuses to be
// read travels along as an error rather than stopping the caller, because in
// a diagnosis that is a finding.
func (m *Manager) Diagnostics() doctor.Input {
	in := doctor.Input{Hosts: doctor.HostsState{Path: m.HostsFile}}

	reg, err := LoadRegistry(m.Home)
	if err != nil {
		in.RegistryErr = err
	} else {
		for _, name := range reg.Names() {
			in.EnvDirs = append(in.EnvDirs, reg[name].Path)
		}
		in.Hosts.Wanted = wantedHosts(reg)
	}

	body, err := hosts.Read(m.HostsFile)
	if err != nil {
		in.Hosts.Err = err
	} else {
		in.Hosts.Present = hosts.Entries(body)
	}
	return in
}
