package internal

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"time"
)

// SenderWorker consome itens da SendQueue e tenta enviá-los ao socket /data.
// Faz compressão opcional e retry, similar ao comportamento Cosmog-style.
func SenderWorker(ctx context.Context, idx int) {
	logger := NewLogger()
	logger.Infof("[worker%d] started", idx)

	for {
		select {
		case <-ctx.Done():
			logger.Infof("[worker%d] stopping", idx)
			return
		case item := <-SendQueue:
			if len(item.Payload) == 0 {
				logger.Warnf("[worker%d] empty payload for %s", idx, item.Path)
				_ = os.Remove(item.Path)
				continue
			}

			// Step 1: compress payload if enabled
			payload := item.Payload
			if cfgUseCompression() {
				var buf bytes.Buffer
				gz := gzip.NewWriter(&buf)
				if _, err := gz.Write(payload); err == nil {
					_ = gz.Close()
					payload = buf.Bytes()
				} else {
					_ = gz.Close()
					logger.Warnf("[worker%d] compression failed: %v", idx, err)
				}
			}

			// Step 2: send via current websocket connection (dataConn)
			conn := GetDataConn()
			if conn == nil {
				logger.Warnf("[worker%d] no active data connection; requeueing %s", idx, item.Path)
				requeue(item)
				time.Sleep(2 * time.Second)
				continue
			}

			conn.SetWriteDeadline(time.Now().Add(12 * time.Second))
			if err := SafeWriteMessage(conn, 2, payload); err != nil {
				logger.Warnf("[worker%d] write error: %v; requeueing %s", idx, err, filepath.Base(item.Path))
				requeue(item)
			} else {
				logger.Infof("[worker%d] sent %s (%d bytes)", idx, filepath.Base(item.Path), len(payload))
				_ = os.Remove(item.Path)
			}

			time.Sleep(120 * time.Millisecond)
		}
	}
}

// requeue tenta recolocar o item na fila sem travar o worker.
func requeue(it SendItem) {
	go func() {
		select {
		case SendQueue <- it:
		case <-time.After(5 * time.Second):
			// fila cheia, deixa arquivo local para scanner reprocessar
		}
	}()
}

// cfgUseCompression lê variável de ambiente ou config.
func cfgUseCompression() bool {
	val := GetEnv("ROTOM_USE_COMPRESSION", "")
	return val == "1" || val == "true" || val == "True"
}
