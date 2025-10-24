package internal

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"time"
)

// StartTCPReceiver abre um listener em addr (ex: "127.0.0.1:7707").
// Formato esperado por cliente: 4 bytes big-endian length, seguido por payload bytes.
// Cada payload é enfileirado em SendQueue como SendItem (Path empty).
func StartTCPReceiver(ctx context.Context, addr string) error {
	logger := NewLogger()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	logger.Infof("[tcp] listening %s", addr)

	go func() {
		<-ctx.Done()
		logger.Infof("[tcp] context canceled; closing listener")
		ln.Close()
	}()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					logger.Warnf("[tcp] accept error: %v", err)
					time.Sleep(500 * time.Millisecond)
					continue
				}
			}
			logger.Infof("[tcp] conn from %s", conn.RemoteAddr())
			go handleTCPConn(ctx, conn)
		}
	}()

	return nil
}

func handleTCPConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	logger := NewLogger()
	for {
		// read 4-byte length
		var lenBuf [4]byte
		_, err := io.ReadFull(conn, lenBuf[:])
		if err != nil {
			if err != io.EOF {
				logger.Warnf("[tcp] read length error: %v", err)
			}
			return
		}
		n := int(binary.BigEndian.Uint32(lenBuf[:]))
		if n <= 0 || n > 50_000_000 { // sanity cap 50MB
			logger.Warnf("[tcp] invalid frame length: %d", n)
			return
		}
		buf := make([]byte, n)
		_, err = io.ReadFull(conn, buf)
		if err != nil {
			logger.Warnf("[tcp] read payload error: %v", err)
			return
		}

		// build SendItem and enqueue (path empty)
		it := SendItem{Path: "", Payload: buf}
		select {
		case SendQueue <- it:
			logger.Debugf("[tcp] enqueued payload %d bytes", len(buf))
		case <-time.After(3 * time.Second):
			// fallback: write to disk if unable to enqueue
			tmp := "/data/local/tmp/rotom_tcp_pending.bin"
			_ = os.WriteFile(tmp, buf, 0644)
			logger.Warnf("[tcp] queue full; wrote payload to %s", tmp)
		}
	}
}
