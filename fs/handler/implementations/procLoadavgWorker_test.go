package implementations

import (
	"os"
	"strings"
	"testing"
)

func TestLoadavgWorkerSamplerLifecycle(t *testing.T) {
	const id = "loadavg-worker-test"
	pid := uint32(os.Getpid())
	defer ReleaseLoadavgForWorker(id)

	if err := StartLoadavgForWorker(id, pid); err != nil {
		t.Fatal(err)
	}
	data, err := ReadLoadavgForWorker(id, pid)
	if err != nil || !strings.Contains(string(data), "/") {
		t.Fatalf("worker loadavg = %q, %v", data, err)
	}

	owner := workerLoadavgOwner(id)
	loadavgSampler.Lock()
	found := false
	for _, node := range loadavgSampler.nodes {
		if node.owner == owner {
			found = true
		}
	}
	loadavgSampler.Unlock()
	if !found {
		t.Fatal("worker did not own a loadavg sampler")
	}

	ReleaseLoadavgForWorker(id)
	loadavgSampler.Lock()
	for _, node := range loadavgSampler.nodes {
		if node.owner == owner {
			loadavgSampler.Unlock()
			t.Fatal("worker sampler survived unregister")
		}
	}
	loadavgSampler.Unlock()
	if data, err := ReadLoadavgForWorker(id, ^uint32(0)); err == nil || data != nil {
		t.Fatalf("stale tracee loadavg = %q, %v; want no data and error", data, err)
	}
}
