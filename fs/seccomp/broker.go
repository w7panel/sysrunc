package seccomp

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// FDBroker keeps seccomp listeners outside the FS manager process. The socket
// lives in the same mount and network namespaces as the manager and is only
// accessible to root. A separate supervisor must keep the broker alive while
// replacing a crashed manager.
type FDBroker struct {
	mu       sync.Mutex
	listener *net.UnixListener
	fds      map[string]int
}

type brokerMessage struct {
	Op    string   `json:"op,omitempty"`
	Name  string   `json:"name,omitempty"`
	Names []string `json:"names,omitempty"`
	Error string   `json:"error,omitempty"`
}

func NewFDBroker(path string) (*FDBroker, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return &FDBroker{listener: listener, fds: make(map[string]int)}, nil
}

func (b *FDBroker) Close() error {
	err := b.listener.Close()
	b.mu.Lock()
	for name, fd := range b.fds {
		unix.Close(fd)
		delete(b.fds, name)
	}
	b.mu.Unlock()
	return err
}

func (b *FDBroker) Serve() error {
	for {
		conn, err := b.listener.AcceptUnix()
		if err != nil {
			return err
		}
		go b.serveConn(conn)
	}
}

func (b *FDBroker) serveConn(conn *net.UnixConn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	request, received, err := readBrokerMessage(conn)
	if err != nil {
		return
	}
	defer func() {
		for _, fd := range received {
			unix.Close(fd)
		}
	}()
	response := brokerMessage{}
	var returnedFD int = -1
	if request.Name != "" {
		if _, err := parseListenerIdentity(request.Name); err != nil {
			response.Error = err.Error()
		}
	}
	if response.Error == "" {
		b.mu.Lock()
		switch request.Op {
		case "add":
			if request.Name == "" || len(received) != 1 {
				response.Error = "add requires one named listener"
			} else {
				if old, ok := b.fds[request.Name]; ok {
					unix.Close(old)
				}
				b.fds[request.Name] = received[0]
				received = nil
			}
		case "remove":
			if request.Name == "" || len(received) != 0 {
				response.Error = "remove requires a name and no descriptor"
			} else if fd, ok := b.fds[request.Name]; ok {
				unix.Close(fd)
				delete(b.fds, request.Name)
			}
		case "list":
			if len(received) != 0 || request.Name != "" {
				response.Error = "list takes no arguments"
			} else {
				for name := range b.fds {
					response.Names = append(response.Names, name)
				}
				sort.Strings(response.Names)
			}
		case "get":
			if request.Name == "" || len(received) != 0 {
				response.Error = "get requires a name and no descriptor"
			} else if fd, ok := b.fds[request.Name]; ok {
				returnedFD = fd
			} else {
				response.Error = "listener not found"
			}
		default:
			response.Error = "invalid broker operation"
		}
		// Keep the mutex until sendmsg duplicates a returned descriptor.
		_ = writeBrokerMessage(conn, response, returnedFD)
		b.mu.Unlock()
		return
	}
	_ = writeBrokerMessage(conn, response, -1)
}

func writeBrokerMessage(conn *net.UnixConn, message brokerMessage, fd int) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	var rights []byte
	if fd >= 0 {
		rights = unix.UnixRights(fd)
	}
	_, _, err = conn.WriteMsgUnix(data, rights, nil)
	return err
}

func readBrokerMessage(conn *net.UnixConn) (brokerMessage, []int, error) {
	var message brokerMessage
	data := make([]byte, 65536)
	oob := make([]byte, unix.CmsgSpace(4*8))
	n, oobn, flags, _, err := conn.ReadMsgUnix(data, oob)
	if err != nil {
		return message, nil, err
	}
	var fds []int
	controls, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return message, nil, err
	}
	for _, control := range controls {
		got, err := unix.ParseUnixRights(&control)
		if err != nil {
			for _, fd := range fds {
				unix.Close(fd)
			}
			return message, nil, err
		}
		fds = append(fds, got...)
	}
	if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		for _, fd := range fds {
			unix.Close(fd)
		}
		return message, nil, fmt.Errorf("truncated FD broker message")
	}
	if err := json.Unmarshal(data[:n], &message); err != nil {
		for _, fd := range fds {
			unix.Close(fd)
		}
		return message, nil, err
	}
	for _, fd := range fds {
		unix.CloseOnExec(fd)
	}
	return message, fds, nil
}

type BrokerClient struct{ path string }

func NewBrokerClient(path string) *BrokerClient { return &BrokerClient{path: path} }

func (c *BrokerClient) request(op, name string, fd int) (brokerMessage, []int, error) {
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: c.path, Net: "unixpacket"})
	if err != nil {
		return brokerMessage{}, nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := writeBrokerMessage(conn, brokerMessage{Op: op, Name: name}, fd); err != nil {
		return brokerMessage{}, nil, err
	}
	response, fds, err := readBrokerMessage(conn)
	if err != nil {
		return response, nil, err
	}
	if response.Error != "" {
		for _, received := range fds {
			unix.Close(received)
		}
		return response, nil, fmt.Errorf("FD broker: %s", response.Error)
	}
	return response, fds, nil
}

func (c *BrokerClient) Add(fd int, name string) error {
	_, fds, err := c.request("add", name, fd)
	for _, received := range fds {
		unix.Close(received)
	}
	return err
}

func (c *BrokerClient) Remove(name string) error {
	_, fds, err := c.request("remove", name, -1)
	for _, received := range fds {
		unix.Close(received)
	}
	return err
}

func (c *BrokerClient) List() ([]string, error) {
	response, fds, err := c.request("list", "", -1)
	for _, received := range fds {
		unix.Close(received)
	}
	return response.Names, err
}

func (c *BrokerClient) Get(name string) (int, error) {
	_, fds, err := c.request("get", name, -1)
	if err != nil {
		return -1, err
	}
	if len(fds) != 1 {
		for _, fd := range fds {
			unix.Close(fd)
		}
		return -1, fmt.Errorf("FD broker returned %d descriptors", len(fds))
	}
	return fds[0], nil
}

type brokerStore struct {
	mu     sync.Mutex
	client *BrokerClient
	names  map[int]string
}

func newBrokerStore(path string) (*brokerStore, []int, error) {
	client := NewBrokerClient(path)
	names, err := client.List()
	if err != nil {
		return nil, nil, err
	}
	store := &brokerStore{client: client, names: make(map[int]string)}
	var inherited []int
	for _, name := range names {
		if _, err := parseListenerIdentity(name); err != nil {
			for _, fd := range inherited {
				unix.Close(fd)
			}
			return nil, nil, err
		}
		fd, err := client.Get(name)
		if err != nil {
			for _, old := range inherited {
				unix.Close(old)
			}
			return nil, nil, err
		}
		store.names[fd] = name
		inherited = append(inherited, fd)
	}
	return store, inherited, nil
}

func (s *brokerStore) add(fd int, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.names[fd] = name
	return s.client.Add(fd, name)
}

func (s *brokerStore) remove(fd int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := s.names[fd]
	if !ok {
		return nil
	}
	if err := s.client.Remove(name); err != nil {
		return err
	}
	delete(s.names, fd)
	return nil
}

func (s *brokerStore) name(fd int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.names[fd]
}
