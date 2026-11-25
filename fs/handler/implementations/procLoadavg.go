//
// Copyright 2019-2023 Nestybox, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package implementations

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"sync"
	"time"

	"github.com/w7panel/sysrunc/fs/domain"
)

var loadavgWorker struct {
	sync.RWMutex
	reader interface {
		ReadLoadavg(context.Context, string, uint32) ([]byte, error)
		TickerEnabled(string) bool
	}
}

// SetLoadavgWorker lets ticker-enabled containers use the worker's sampler
// from both FUSE and seccomp reads. Containers without ticker keep the local
// handler, preserving the default-off behavior.
func SetLoadavgWorker(reader interface {
	ReadLoadavg(context.Context, string, uint32) ([]byte, error)
	TickerEnabled(string) bool
}) {
	loadavgWorker.Lock()
	loadavgWorker.reader = reader
	loadavgWorker.Unlock()
}

func readLoadavg(req *domain.HandlerRequest) ([]byte, error) {
	loadavgWorker.RLock()
	reader := loadavgWorker.reader
	loadavgWorker.RUnlock()
	if reader != nil && req != nil && req.Container != nil && req.Pid != 0 && reader.TickerEnabled(req.Container.ID()) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err := reader.ReadLoadavg(ctx, req.Container.ID(), req.Pid)
		cancel()
		if err == nil {
			return data, nil
		}
	}
	node := loadavgSamplerForReq(req)
	return []byte(node.format()), nil
}

func workerLoadavgOwner(containerID string) string { return "worker:" + containerID }

// StartLoadavgForWorker starts cgroup and task sampling when an opted-in
// container registers. A missing init process is retried on the first read.
func StartLoadavgForWorker(containerID string, pid uint32) error {
	if containerID == "" || pid == 0 {
		return fmt.Errorf("loadavg requires a container ID and tracee PID")
	}
	req := &domain.HandlerRequest{Pid: pid}
	if err := requireRequestResourceView(req); err != nil {
		return err
	}
	loadavgSamplerForReqOwner(req, workerLoadavgOwner(containerID))
	return nil
}

// ReadLoadavgForWorker returns the ticker worker's container-scoped sample.
func ReadLoadavgForWorker(containerID string, pid uint32) ([]byte, error) {
	if containerID == "" || pid == 0 {
		return nil, fmt.Errorf("loadavg requires a container ID and tracee PID")
	}
	req := &domain.HandlerRequest{Pid: pid}
	if err := requireRequestResourceView(req); err != nil {
		return nil, err
	}
	node := loadavgSamplerForReqOwner(req, workerLoadavgOwner(containerID))
	node.RLock()
	closed := node.closed
	node.RUnlock()
	if closed {
		return nil, fmt.Errorf("loadavg sampler for %s is closed", containerID)
	}
	return []byte(node.format()), nil
}

// ReleaseLoadavgForWorker stops every sampled cgroup view owned by a container.
func ReleaseLoadavgForWorker(containerID string) {
	loadavgSampler.release(workerLoadavgOwner(containerID))
}

type loadavgNode struct {
	sync.RWMutex
	key        string
	pidfd      *os.File
	cgroupKey  string
	pidns      string
	viewCgroup cgroupView
	owner      string
	closed     bool
	refreshing bool
	stop       chan struct{}
	samplePID  int
	avenrun    [3]uint64
	running    int
	total      int
	lastPID    int
}

type loadavgSamplerState struct {
	sync.Mutex
	nodes  map[string]*loadavgNode
	sample func(int, cgroupView, string) (int, int, int)
}

var loadavgSampler = &loadavgSamplerState{
	nodes:  make(map[string]*loadavgNode),
	sample: func(_ int, cg cgroupView, ns string) (int, int, int) { return loadavgStatsForView(cg, ns) },
}

func loadavgSamplerForReq(req *domain.HandlerRequest) *loadavgNode {
	owner := ""
	if req != nil {
		owner = resourceOwnerKey(req)
	}
	return loadavgSamplerForReqOwner(req, owner)
}

func loadavgSamplerForReqOwner(req *domain.HandlerRequest, owner string) *loadavgNode {
	key, samplePID, viewCgroup, viewNS := loadavgNodeKey(req)
	key = owner + ":" + key

	loadavgSampler.Lock()
	if req != nil && req.ResourceOwner != nil && req.ResourceOwner.Closed() {
		loadavgSampler.Unlock()
		return &loadavgNode{closed: true, total: 1}
	}
	node := loadavgSampler.nodes[key]
	if node == nil {
		fd, err := unix.PidfdOpen(samplePID, 0)
		ns, nsErr := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", samplePID))
		if err != nil || nsErr != nil || ns != viewNS {
			if err == nil {
				unix.Close(fd)
			}
			loadavgSampler.Unlock()
			return &loadavgNode{closed: true, total: 1}
		}
		node = &loadavgNode{
			key: key, cgroupKey: cgroupViewKey(pruneInitScopeCgroup(cgroupForPid(uint32(samplePID)))), pidfd: os.NewFile(uintptr(fd), "loadavg-pid"), pidns: ns,
			viewCgroup: viewCgroup,
			stop:       make(chan struct{}),
			owner:      owner,
			samplePID:  samplePID,
			total:      1,
			lastPID:    namespacePID(samplePID),
		}
		loadavgSampler.nodes[key] = node
		go loadavgSampler.run(node)
	}
	loadavgSampler.Unlock()

	return node
}

// Each used resource view samples independently until its FUSE owner exits.
// Nested cgroups sharing a FUSE owner must retain separate load averages.
func (s *loadavgSamplerState) run(node *loadavgNode) {
	ticker := time.NewTicker(loadavgSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-node.stop:
			return
		case <-ticker.C:
			s.refresh(node)
		}
	}
}

func (s *loadavgSamplerState) release(owner string) {
	s.Lock()
	defer s.Unlock()
	for key, node := range s.nodes {
		if node.owner == owner {
			node.Lock()
			node.close()
			node.Unlock()
			delete(s.nodes, key)
		}
	}
}

func (s *loadavgSamplerState) refresh(node *loadavgNode) {
	node.Lock()
	if node.closed || node.refreshing {
		node.Unlock()
		return
	}
	if !node.alive() {
		node.Unlock()
		s.expire(node)
		return
	}
	node.refreshing = true
	samplePID := node.samplePID
	viewCgroup, viewNS := node.viewCgroup, node.pidns
	node.Unlock()

	total, running, lastPID := s.sample(samplePID, viewCgroup, viewNS)
	total, running = normalizeLoadavgSample(total, running)
	if lastPID == 0 {
		lastPID = namespacePID(samplePID)
	}

	node.Lock()
	node.refreshing = false
	if node.closed {
		node.Unlock()
		return
	}
	if !node.alive() {
		node.Unlock()
		s.expire(node)
		return
	}
	defer node.Unlock()
	node.avenrun[0] = calcLoadavg(node.avenrun[0], loadavgExp1, uint64(running))
	node.avenrun[1] = calcLoadavg(node.avenrun[1], loadavgExp5, uint64(running))
	node.avenrun[2] = calcLoadavg(node.avenrun[2], loadavgExp15, uint64(running))
	node.running = running
	node.total = total
	node.lastPID = lastPID
}

func normalizeLoadavgSample(total, running int) (int, int) {
	if total == 0 {
		total = 1
	}
	if running > total {
		total = running
	}
	return total, running
}

func calcLoadavg(load, exp, active uint64) uint64 {
	if active > 0 {
		active *= loadavgFixed1
	}
	newLoad := load*exp + active*(loadavgFixed1-exp)
	if active >= load {
		newLoad += loadavgFixed1 - 1
	}
	return newLoad / loadavgFixed1
}

func (n *loadavgNode) format() string {
	n.RLock()
	a := n.avenrun[0] + loadavgFixed1/200
	b := n.avenrun[1] + loadavgFixed1/200
	c := n.avenrun[2] + loadavgFixed1/200
	running := n.running
	total := n.total
	lastPID := n.lastPID
	n.RUnlock()

	if total == 0 {
		total = 1
	}

	return fmt.Sprintf("%d.%02d %d.%02d %d.%02d %d/%d %d\n",
		loadavgInt(a), loadavgFrac(a),
		loadavgInt(b), loadavgFrac(b),
		loadavgInt(c), loadavgFrac(c),
		running, total, lastPID)
}

func loadavgInt(v uint64) uint64 {
	return v >> loadavgFShift
}

func loadavgFrac(v uint64) uint64 {
	return loadavgInt((v & (loadavgFixed1 - 1)) * 100)
}

// Caller holds the node lock; pidfd detects exit even after numeric PID reuse.
func (n *loadavgNode) alive() bool {
	if n.pidfd == nil {
		return true
	} // Synthetic samplers used by unit tests.
	fds := []unix.PollFd{{Fd: int32(n.pidfd.Fd()), Events: unix.POLLIN}}
	count, err := unix.Poll(fds, 0)
	if err != nil || count != 0 {
		return false
	}
	ns, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", n.samplePID))
	return err == nil && ns == n.pidns && cgroupViewKey(pruneInitScopeCgroup(cgroupForPid(uint32(n.samplePID)))) == n.cgroupKey
}
func (n *loadavgNode) close() {
	if n.closed {
		return
	}
	n.closed = true
	if n.stop != nil {
		close(n.stop)
	}
	if n.pidfd != nil {
		n.pidfd.Close()
	}
}
func (s *loadavgSamplerState) expire(node *loadavgNode) {
	s.Lock()
	defer s.Unlock()
	node.Lock()
	defer node.Unlock()
	node.close()
	if s.nodes[node.key] == node {
		delete(s.nodes, node.key)
	}
}
