module github.com/w7panel/sysrunc/fs

go 1.25.0

require (
	bazil.org/fuse v0.0.0-00010101000000-000000000000
	github.com/coreos/go-systemd v0.0.0-20191104093116-d3cd4ed1dbcf
	github.com/hashicorp/go-immutable-radix v1.3.0
	github.com/w7panel/sysrunc/ipc v0.0.0-00010101000000-000000000000
	github.com/w7panel/sysrunc/libs/capability v0.0.0-00010101000000-000000000000
	github.com/w7panel/sysrunc/libs/formatter v0.0.0-20210709231355-1ea69f2f6dbb
	github.com/w7panel/sysrunc/libs/linuxUtils v0.0.0-00010101000000-000000000000
	github.com/w7panel/sysrunc/libs/pidfd v0.0.0-00010101000000-000000000000
	github.com/w7panel/sysrunc/libs/utils v0.0.0-00010101000000-000000000000
	github.com/w7panel/sysrunc/runc v0.0.0-00010101000000-000000000000
	github.com/pkg/profile v1.5.0
	github.com/seccomp/libseccomp-golang v0.10.0
	github.com/sirupsen/logrus v1.9.4
	github.com/spf13/afero v1.4.1
	github.com/stretchr/testify v1.11.1
	github.com/urfave/cli v1.22.14
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/sync v0.20.0
	golang.org/x/sys v0.45.0
	google.golang.org/grpc v1.82.1
	gopkg.in/hlandau/service.v1 v1.0.7
)

require (
	cyphar.com/go-pathrs v0.2.1 // indirect
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/checkpoint-restore/go-criu/v4 v4.1.0 // indirect
	github.com/cilium/ebpf v0.17.3 // indirect
	github.com/containerd/console v1.0.5 // indirect
	github.com/coreos/go-systemd/v22 v22.1.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.2 // indirect
	github.com/cyphar/filepath-securejoin v0.6.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/deckarep/golang-set v1.8.0 // indirect
	github.com/deckarep/golang-set/v2 v2.3.1 // indirect
	github.com/godbus/dbus/v5 v5.0.3 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/hashicorp/go-uuid v1.0.1 // indirect
	github.com/hashicorp/golang-lru v0.5.1 // indirect
	github.com/joshlf/go-acl v0.0.0-20200411065538-eae00ae38531 // indirect
	github.com/karrick/godirwalk v1.16.1 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/moby/moby/client v0.5.0 // indirect
	github.com/moby/sys/mountinfo v0.4.0 // indirect
	github.com/mrunalp/fileutils v0.5.0 // indirect
	github.com/w7panel/sysrunc/libs/idMap v0.0.0-00010101000000-000000000000 // indirect
	github.com/w7panel/sysrunc/libs/idShiftUtils v0.0.0-00010101000000-000000000000 // indirect
	github.com/w7panel/sysrunc/libs/mount v0.0.0-20240602025437-33cbdf5a9e98 // indirect
	github.com/w7panel/sysrunc/libs/overlayUtils v0.0.0-00010101000000-000000000000 // indirect
	github.com/opencontainers/runtime-spec v1.1.1-0.20230823135140-4fec88fd00a4 // indirect
	github.com/opencontainers/selinux v1.13.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/vishvananda/netns v0.0.0-20191106174202-0a2b9b5464df // indirect
	github.com/willf/bitset v1.1.11 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/godbus/dbus => github.com/godbus/dbus/v5 v5.0.3
)
