package internal

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
)

// ControlLoop conecta ao endpoint /control e envia heartbeats periódicos.
// Ele reconecta automaticamente com backoff em caso de falhas.
// Caso chegue uma mensagem de controle (texto/json), ela é logada e
// pode ser expandida para executar comandos (ex: toggle, reload hooks).
func ControlLoop(ctx context.Context, cfg Config) {
	logger := NewLogger()
	logger.Infof("[control] starting; endpoint=%s", cfg.ControlEndpoint())

	dialer := websocket.DefaultDialer
	headers := map[string][]string{}
	if cfg.Rotom.Secret != "" {
		headers["Authorization"] = []string{"Bearer " + cfg.Rotom.Secret}
	}

	backoff := 1 * time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			logger.Info("[control] context canceled; exiting")
			return
		default:
		}

		logger.Infof("[control] dialing %s ...", cfg.ControlEndpoint())
		conn, resp, err := dialer.Dial(cfg.ControlEndpoint(), headers)
		if err != nil {
			if resp != nil {
				logger.Errorf("[control] dial error: %v (http %s)", err, resp.Status)
			} else {
				logger.Errorf("[control] dial error: %v", err)
			}
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// success
		logger.Info("[control] connected")
		backoff = 1 * time.Second

		// send intro once
		intro := map[string]any{
			"deviceId": cfg.General.DeviceName,
			"version":  2,
			"origin":   "lab",
			"publicIp": "127.0.0.1",
			"secret":   cfg.Rotom.Secret,
		}
		if b, err := json.Marshal(intro); err == nil {
			_ = conn.WriteMessage(websocket.TextMessage, b)
			logger.Info("[control] intro sent")
		} else {
			logger.Warnf("[control] intro marshal failed: %v", err)
		}

		// reader goroutine
		readErrCh := make(chan error, 1)
		go func(c *websocket.Conn) {
			defer close(readErrCh)
			for {
				_, msg, err := c.ReadMessage()
				if err != nil {
					readErrCh <- err
					return
				}
				// handle control message (json or text)
				logger.Infof("[control] recv: %s", string(msg))

				// Example: basic command handling (toggle debug or reload hooks)
				var m map[string]any
				if err := json.Unmarshal(msg, &m); err == nil {
					if cmd, ok := m["cmd"].(string); ok {
						switch cmd {
						case "reload_hooks":
							logger.Info("[control] reload_hooks command received; reloading libs")
							// simplistic reload: unload and attempt to reload paths in ROTOM_LIBS
							// (user libs reloaded only if variable set)
							ReloadHookLibsFromEnv()
						case "status":
							// respond with a simple status message
							status := map[string]any{
								"type":    "status",
								"workers": cfg.General.Workers,
								"device":  cfg.General.DeviceName,
							}
							if bb, err := json.Marshal(status); err == nil {
								_ = c.WriteMessage(websocket.TextMessage, bb)
							}
						}
					}
				}
			}
		}(conn)

		// heartbeat loop
		ticker := time.NewTicker(15 * time.Second)
		closed := false
		for !closed {
			select {
			case <-ctx.Done():
				logger.Info("[control] context canceled -> closing connection")
				conn.Close()
				closed = true
			case err := <-readErrCh:
				logger.Warnf("[control] read loop ended: %v", err)
				conn.Close()
				closed = true
			case <-ticker.C:
				hb := map[string]any{
					"type":     "heartbeat",
					"ts":       time.Now().Unix(),
					"workerId": cfg.General.DeviceName,
				}
				if b, err := json.Marshal(hb); err == nil {
					conn.SetWriteDeadline(time.Now().Add(8 * time.Second))
					if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
						logger.Warnf("[control] heartbeat write failed: %v", err)
						conn.Close()
						closed = true
					} else {
						logger.Debug("[control] heartbeat sent")
					}
				}
			}
		}

		// small pause before reconnect
		ticker.Stop()
		time.Sleep(800 * time.Millisecond)
	}
}

// ReloadHookLibsFromEnv is a small helper that unloads current hook libs and attempts to reload
// the colon-separated paths in ROTOM_LIBS environment variable.
func ReloadHookLibsFromEnv() {
	logger := NewLogger()
	// unload all
	for _, h := range LoadedHookLibs {
		// nothing to call for unload via cgo wrapper; just drop references
		_ = h
	}
	LoadedHookLibs = nil

	val := GetEnv("ROTOM_LIBS", "")
	if val == "" {
		logger.Info("[control] ROTOM_LIBS empty; nothing to load")
		return
	}
	parts := splitPaths(val)
	for _, p := range parts {
		if p == "" {
			continue
		}
		if err := LoadHookLib(p); err != nil {
			logger.Errorf("[control] reload load failed %s: %v", p, err)
		}
	}
}

// small helpers used above
func splitPaths(s string) []string {

	for _, p := range []rune(s) {
		_ = p
	}
	// simple colon split
	for _, part := range []string{} {
		_ = part
	}
	// actually do split:
	return splitColon(s)
}
func splitColon(s string) []string {
	if s == "" {
		return []string{}
	}
	var res []string
	curr := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			res = append(res, curr)
			curr = ""
			continue
		}
		curr += string(s[i])
	}
	if curr != "" {
		res = append(res, curr)
	}
	return res
}
