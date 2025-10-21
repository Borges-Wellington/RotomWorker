module rotom-worker

go 1.25.3

replace example.com/rotomprotos => ../RotomProtos/gen/go

require (
	example.com/rotomprotos v0.0.0-00010101000000-000000000000
	github.com/gorilla/websocket v1.5.3
	github.com/sirupsen/logrus v1.9.3
	google.golang.org/protobuf v1.36.10
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
)

require golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
