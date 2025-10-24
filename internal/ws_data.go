package internal

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	dataConn     *websocket.Conn
	dataConnLock = make(chan struct{}, 1)
	writeLock    sync.Mutex // 🔒 protege todas as escritas simultâneas
)

// wsConnAdapter adapta *websocket.Conn para ser compatível com SendWelcome
type wsConnAdapter struct {
	c *websocket.Conn
}


// GetDataConn retorna a conexão WebSocket de dados atual (thread-safe).
func GetDataConn() *websocket.Conn {
	select {
	case dataConnLock <- struct{}{}:
		defer func() { <-dataConnLock }()
		return dataConn
	default:
		return dataConn
	}
}

// Define a conexão global atual.
func setDataConn(c *websocket.Conn) {
	select {
	case dataConnLock <- struct{}{}:
		dataConn = c
		<-dataConnLock
	default:
		dataConn = c
	}
}

// 🔒 Função de escrita segura (adicione AQUI, logo após os getters)
func SafeWriteMessage(conn *websocket.Conn, messageType int, data []byte) error {
	writeLock.Lock()
	defer writeLock.Unlock()
	return conn.WriteMessage(messageType, data)
}


func (a *wsConnAdapter) WriteBinary(b []byte) error {
	a.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return SafeWriteMessage(a.c, websocket.BinaryMessage, b)
}

// StartDataWs abre/gerencia a conexão websocket de dados (/data).
// Ele consome SendQueue e envia cada item como mensagem binária.
// ctx: cancelation contexto do programa.
// cfg: configuração (usa cfg.DataEndpoint() e cfg.Rotom.Secret).
func StartDataWs(ctx context.Context, cfg Config) {
	logger := NewLogger()
	
	dataURL := cfg.DataEndpoint()
	if dataURL == "" {
		logger.Error("[data] data endpoint vazio, abortando StartDataWs")
		return
	}

	// ensure endpoint ends with /data (DataEndpoint already normaliza, but vamos garantir)
	// dialer and headers
	dialer := websocket.DefaultDialer

	// headers - Authorization if present
	headers := http.Header{}
	if cfg.Rotom.Secret != "" {
		headers.Set("Authorization", "Bearer "+cfg.Rotom.Secret)
	}

	// reconnect/backoff params
	backoff := 1 * time.Second
	const maxBackoff = 30 * time.Second

	for {
		// check exit
		select {
		case <-ctx.Done():
			logger.Info("[data] context canceled, exiting StartDataWs")
			return
		default:
		}

		logger.Infof("[data] connecting to %s ...", dataURL)
		conn, resp, err := dialer.Dial(dataURL, headers)
		if err != nil {
			// show http response if available (helpful)
			if resp != nil {
				logger.Errorf("[data] dial failed: %v (http status: %s)", err, resp.Status)
			} else {
				logger.Errorf("[data] dial failed: %v", err)
			}
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		setDataConn(conn)
		logger.Info("[data] connected")

        // Envia WelcomeMessage protobuf assim que conectar
        _ = SendWelcome(&wsConnAdapter{c: conn}, cfg, logger)

		backoff = 1 * time.Second // reset on success

		// channel to signal reader goroutine exit
		msgReadStop := make(chan struct{})
		// reader goroutine
		go func(c *websocket.Conn) {
			defer close(msgReadStop)
			for {
				_, msg, err := c.ReadMessage()
				if err != nil {
					logger.Warnf("[data] read error: %v", err)
					return
				}

				// 1️⃣ Primeiro, deixe os hooks ELF de resposta tentarem processar
				if out, err := TryProcessResponse(msg); err == nil && len(out) > 0 {
					logger.Debug("[data] HandleResponse produced output; forwarding to data WS")
					conn2 := GetDataConn()
					if conn2 != nil {
						conn2.SetWriteDeadline(time.Now().Add(10 * time.Second))
						_ = SafeWriteMessage(conn2, websocket.BinaryMessage, out)
					}
					continue
				}

				// 2️⃣ Depois tente os hooks Go internos
				handledReq, _, errReq := TryHandleRequest(msg)
				if errReq == nil && handledReq {
					logger.Debug("[data] incoming message handled by HandleRequest hook")
					continue
				}

				handledResp, outResp, errResp := TryHandleResponse(msg)
				if errResp == nil && handledResp {
					logger.Debug("[data] incoming message handled by HandleResponse hook")
					_ = outResp
					continue
				}

				// 3️⃣ Caso nada trate, apenas loga
				logger.Debugf("[data] incoming message (len=%d)", len(msg))
			}
		}(conn)


		// writer loop: consume SendQueue and write to socket
		// note: read goroutine will close msgReadStop if conn dies
		writerLoop:
		for {
			select {
			case <-ctx.Done():
				logger.Info("[data] context canceled -> close connection and exit")
				setDataConn(nil)
				conn.Close()
				<-msgReadStop
				return
			case <-msgReadStop:
				logger.Warn("[data] reader goroutine ended; will reconnect")
				setDataConn(nil)
				conn.Close()
				break writerLoop
			case item := <-SendQueue:

				// First offer the raw payload to ELF hooks (HandleRequest). If a hook
				// returns a buffer, send that instead and skip the normal compression step.
				if out, err := TryProcessRequest(item.Payload); err == nil && len(out) > 0 {
					logger.Debugf("[data] hook processed SendItem %s -> %d bytes; sending hook output", filepath.Base(item.Path), len(out))
					// write hook output using the safe writer
					conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
					if err := SafeWriteMessage(conn, websocket.BinaryMessage, out); err != nil {
						logger.Warnf("[data] write hook output failed: %v; requeueing %s", err, item.Path)
						requeue(item) // use your existing requeue helper (non-blocking)
						// close & reconnect to force fresh connection
						conn.Close()
						setDataConn(nil)
						<-msgReadStop
						break writerLoop
					} else {
						// success: remove the file and continue to next item
						if item.Path != "" {
							_ = os.Remove(item.Path)
							logger.Infof("[data] sent hook output and removed %s (%d bytes)", filepath.Base(item.Path), len(out))
						} else {
							logger.Infof("[data] sent hook output (%d bytes)", len(out))
						}
					}
					// skip normal sending for this item
					continue
				}

				// If queue delivered a zero-value item (shouldn't happen) skip
				if len(item.Payload) == 0 {
					logger.Warn("[data] got empty SendItem payload; skipping")
					// remove file to avoid infinite loop? keep original behaviour: try remove if exists
					_ = os.Remove(item.Path)
					continue
				}

				// prepare payload (compress if requested)
				payload := item.Payload
				if cfg.Rotom.UseCompression {
					var buf bytes.Buffer
					gw := gzip.NewWriter(&buf)
					_, err := gw.Write(payload)
					_ = gw.Close()
					if err == nil {
						payload = buf.Bytes()
					} else {
						logger.Warnf("[data] gzip compress failed: %v (sending uncompressed)", err)
					}
				}

				// try to write; set a write deadline
				conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
				err := SafeWriteMessage(conn, websocket.BinaryMessage, payload)
				if err != nil {
					logger.Errorf("[data] write message failed: %v; requeueing %s", err, item.Path)
					// requeue non-blocking: spawn goroutine to push back after small delay (avoid deadlock)
					go func(it SendItem) {
						time.Sleep(1 * time.Second)
						// attempt to requeue within bounded time
						select {
						case SendQueue <- it:
							// requeued
						case <-time.After(5 * time.Second):
							// if channel full, leave file on disk (scanner will keep it)
							logger.Warnf("[data] could not requeue item (channel full), leaving file: %s", it.Path)
						}
					}(item)
					// close conn and reconnect (reader goroutine will notice closure/err)
					conn.Close()
					setDataConn(nil)
					<-msgReadStop
					break writerLoop
				} else {
					// success: remove local file
					if item.Path != "" {
						if err := os.Remove(item.Path); err != nil {
							logger.Warnf("[data] sent but failed to remove file %s: %v", item.Path, err)
						} else {
							logger.Infof("[data] sent and removed %s (%d bytes)", filepath.Base(item.Path), len(item.Payload))
						}
					} else {
						logger.Infof("[data] sent payload (%d bytes) (no file path)", len(item.Payload))
					}
				}

			}
		}

		// short sleep before reconnect to avoid busy-loop
		time.Sleep(800 * time.Millisecond)
	}
}
