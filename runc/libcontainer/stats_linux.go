package libcontainer

import (
	"github.com/w7panel/sysrunc/runc/libcontainer/cgroups"
	"github.com/w7panel/sysrunc/runc/libcontainer/intelrdt"
	"github.com/w7panel/sysrunc/runc/types"
)

type Stats struct {
	Interfaces    []*types.NetworkInterface
	CgroupStats   *cgroups.Stats
	IntelRdtStats *intelrdt.Stats
}
