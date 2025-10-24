package internal

import (
	"github.com/sirupsen/logrus"
	pb "rotomworker/proto_gen"
	"google.golang.org/protobuf/proto"
)

// WebSocketSender é uma interface mínima que abstrai o envio binário.
type WebSocketSender interface {
	WriteBinary([]byte) error
}


func SendWelcome(conn WebSocketSender, cfg Config, logger *logrus.Logger) error {
	w := &pb.WelcomeMessage{
		WorkerId:    cfg.General.DeviceName,
		Origin:      "lab",
		VersionCode: 2,
		VersionName: "rotom-worker-go-hybrid",
		Useragent:   "rotom-worker-go-hybrid/1.0",
		DeviceId:    cfg.General.DeviceName + "-device",
	}
	data, err := proto.Marshal(w)
	if err != nil {
		return err
	}
	if err := conn.WriteBinary(data); err != nil {
		return err
	}
	logger.Infof("[data] sent WelcomeMessage (%d bytes)", len(data))
	return nil
}
