package internal

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

func StartWorker(ctx context.Context, log *logrus.Logger) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Debug("Worker ativo — aguardando dados do hook...")
			// TODO: comunicação com o socket rotom + envio dos dados capturados
		case <-ctx.Done():
			log.Info("Worker encerrado.")
			return
		}
	}
}
