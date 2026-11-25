package implementations

import "github.com/w7panel/sysrunc/fs/domain"

// ReleaseResourceOwner runs after the last FUSE server using this state exits.
func ReleaseResourceOwner(owner *domain.ResourceOwner) {
	owner.Close()
	ownerKey := resourceOwnerKey(&domain.HandlerRequest{ResourceOwner: owner})
	releaseResourceSnapshots(ownerKey)
	loadavgSampler.release(ownerKey)
	releaseProcStatOwner(ownerKey)
}

func releaseProcStatOwner(ownerKey string) {
	procStatStatesMu.Lock()
	defer procStatStatesMu.Unlock()
	for key, state := range procStatStates {
		if state.owner == ownerKey {
			delete(procStatStates, key)
		}
	}
}
