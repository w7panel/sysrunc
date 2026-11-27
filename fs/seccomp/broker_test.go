package seccomp

import (
	"errors"
	"net"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFDBrokerRetainsDescriptorAcrossClientClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	broker, err := NewFDBroker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	done := make(chan error, 1)
	go func() { done <- broker.Serve() }()
	defer func() {
		broker.Close()
		if err := <-done; err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("broker serve: %v", err)
		}
	}()

	name, err := (listenerIdentity{container: "container", initPID: 11, initStart: 22, pid: 33, start: 44}).name()
	if err != nil {
		t.Fatal(err)
	}
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[1])
	client := NewBrokerClient(path)
	if err := client.Add(pipe[0], name); err != nil {
		t.Fatal(err)
	}
	unix.Close(pipe[0])
	if names, err := client.List(); err != nil || len(names) != 1 || names[0] != name {
		t.Fatalf("stored names = %v, %v", names, err)
	}
	store, inherited, err := newBrokerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(inherited) != 1 || store.name(inherited[0]) != name {
		t.Fatalf("restored descriptors = %v, name = %q", inherited, store.name(inherited[0]))
	}
	fd := inherited[0]
	defer unix.Close(fd)
	if _, err := unix.Write(pipe[1], []byte("retained")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 8)
	if n, err := unix.Read(fd, data); err != nil || n != len(data) || string(data) != "retained" {
		t.Fatalf("recovered fd read %q (%d): %v", data, n, err)
	}
	if err := store.remove(fd); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(name); err == nil {
		t.Fatal("removed descriptor remained available")
	}
}
