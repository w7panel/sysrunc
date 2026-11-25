module github.com/w7panel/sysrunc/ipc

go 1.24

require (
	github.com/golang/protobuf v1.5.4
	github.com/sirupsen/logrus v1.9.3
	github.com/w7panel/sysrunc/libs/formatter v0.0.0-00010101000000-000000000000
	golang.org/x/sys v0.33.0
	google.golang.org/grpc v1.63.0
	google.golang.org/protobuf v1.34.1
)

require (
	github.com/moby/moby/client v0.5.0 // indirect
	golang.org/x/net v0.23.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240227224415-6ceb2ff114de // indirect
)

replace (
	github.com/godbus/dbus => github.com/godbus/dbus/v5 v5.0.3
	github.com/w7panel/sysrunc/libs/formatter => ../libs/formatter
)
