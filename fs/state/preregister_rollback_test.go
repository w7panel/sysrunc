package state

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/w7panel/sysrunc/fs/domain"
	"github.com/w7panel/sysrunc/fs/mocks"
	"github.com/w7panel/sysrunc/fs/sysio"
)

func TestPreRegisterRollsBackContainerWhenFuseWorkerFails(t *testing.T) {
	servers := &mocks.FuseServerServiceIface{}
	servers.On("CreateFuseServer", mock.Anything, mock.Anything).Return(errors.New("worker unavailable")).Once()
	css := &containerStateService{
		idTable: make(map[string]*container), netnsTable: make(map[domain.Inode][]*container),
		fss: servers, ios: sysio.NewIOService(domain.IOOsFileService),
	}
	if err := css.ContainerPreRegister("one", "/proc/self/ns/net"); err == nil {
		t.Fatal("pre-registration succeeded without a mountpoint")
	}
	if _, exists := css.idTable["one"]; exists {
		t.Fatal("failed pre-registration retained the container")
	}
	if len(css.netnsTable) != 0 {
		t.Fatalf("failed pre-registration retained namespace tracking: %+v", css.netnsTable)
	}
	servers.AssertExpectations(t)
}
