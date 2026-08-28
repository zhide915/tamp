package router

import (
	"net"
	"testing"
)

func TestAListenedPortReadsAsTaken(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port

	if portIsFree(port) {
		t.Errorf("port %d has a listener on it and read as free", port)
	}
}

func TestAPortNobodyIsServingReadsAsFree(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	// Listen-then-close yields a port known to be usable and unoccupied.
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	if !portIsFree(port) {
		t.Errorf("port %d has nothing on it and read as taken", port)
	}
}
