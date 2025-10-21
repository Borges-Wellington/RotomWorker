// main.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	rotompb "example.com/rotomprotos"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	Rotom struct {
		WorkerEndpoint string `json:"worker_endpoint"`
		DeviceEndpoint string `json:"device_endpoint"`
		Secret         string `json:"secret"`
		UseCompression bool   `json:"use_compression"`
	} `json:"rotom"`

	General struct {
		DeviceName string `json:"device_name"`
		Workers    int    `json:"workers"`
		DNSServer  string `json:"dns_server"`
		ScanDir    string `json:"scan_dir"`
	} `json:"general"`

	Log struct {
		Level      string `json:"level"`
		UseColors  bool   `json:"use_colors"`
		LogToFile  bool   `json:"log_to_file"`
		MaxSize    int    `json:"max_size"`
		MaxBackups int    `json:"max_backups"`
		MaxAge     int    `json:"max_age"`
		Compress   bool   `json:"compress"`
		FilePath   string `json:"file_path"`
	} `json:"log"`

	Tuning struct {
		WorkerSpawnDelayMs int `json:"worker_spawn_delay_ms"`
	} `json:"tuning"`
}

type controlIntro struct {
	DeviceId string `json:"deviceId"`
	Version  int    `json:"version"`
	Origin   string `json:"origin"`
	PublicIp string `json:"publicIp"`
	Secret   string `json:"secret"`
}

type controlHeartbeat struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"ts"`
	WorkerId  string `json:"workerId"`
}

type SendItem struct {
	Path    string
	Request []byte
	// Response []byte // reserved if you want to send responses later
}

var wsWriteMu sync.Mutex

var (
	flagConfig = flag.String("config", "/data/local/tmp/rotom-config.json", "path to config.json")
)

func setupLogger(cfg *Config) *logrus.Logger {
	log := logrus.New()

	// level
	level, err := logrus.ParseLevel(cfg.Log.Level)
	if err != nil {
		level = logrus.InfoLevel
	}
	log.SetLevel(level)

	// formatter
	formatter := &logrus.TextFormatter{
		DisableColors:   !cfg.Log.UseColors,
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339,
	}
	log.SetFormatter(formatter)

	// output
	if cfg.Log.LogToFile && cfg.Log.FilePath != "" {
		l := &lumberjack.Logger{
			Filename:   cfg.Log.FilePath,
			MaxSize:    cfg.Log.MaxSize,    // megabytes
			MaxBackups: cfg.Log.MaxBackups, // files
			MaxAge:     cfg.Log.MaxAge,     // days
			Compress:   cfg.Log.Compress,
		}
		log.SetOutput(io.MultiWriter(os.Stdout, l))
	} else {
		log.SetOutput(os.Stdout)
	}

	return log
}

func readConfig(path string) (*Config, error) {
	bs, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(bs, &cfg); err != nil {
		return nil, err
	}
	// defaults
	if cfg.General.ScanDir == "" {
		cfg.General.ScanDir = "/data/data/com.nianticlabs.pokemongo/cache"
	}
	if cfg.General.DeviceName == "" {
		cfg.General.DeviceName = "android-device"
	}
	if cfg.General.Workers <= 0 {
		cfg.General.Workers = 1
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.FilePath == "" {
		cfg.Log.FilePath = "/data/local/tmp/rotom-worker.log"
	}
	if cfg.Tuning.WorkerSpawnDelayMs == 0 {
		cfg.Tuning.WorkerSpawnDelayMs = 500
	}
	return &cfg, nil
}

// WS dial helper that adds Authorization header
func dialWS(urlWithPath, secret string) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if secret != "" {
		header.Add("Authorization", "Bearer "+secret)
	}
	c, resp, err := websocket.DefaultDialer.Dial(urlWithPath, header)
	return c, resp, err
}

// Control loop: connect to /control, send intro JSON, send heartbeat periodically, print incoming control messages
func controlLoop(ctx context.Context, wg *sync.WaitGroup, baseURL, secret, workerID string, hbInterval time.Duration, log *logrus.Logger) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		backoff := time.Second * 3
		for {
			select {
			case <-ctx.Done():
				log.Info("[control] shutting down control loop")
				return
			default:
			}

			full := fmt.Sprintf("%s/control", baseURL)
			conn, resp, err := dialWS(full, secret)
			if err != nil {
				log.Warnf("[control] dial %s failed: %v (resp=%v) — retrying in %s", full, err, resp, backoff)
				time.Sleep(backoff)
				if backoff < 30*time.Second {
					backoff += time.Second
				}
				continue
			}
			log.Info("[control] connected")

			intro := controlIntro{
				DeviceId: workerID,
				Version:  1,
				Origin:   "lab",
				PublicIp: "127.0.0.1",
				Secret:   secret,
			}
			if err := conn.WriteJSON(intro); err != nil {
				log.Warnf("[control] write intro failed: %v", err)
				_ = conn.Close()
				time.Sleep(backoff)
				continue
			}
			log.Infof("[control] intro sent for worker=%s", workerID)
			backoff = time.Second * 3 // reset

			// heartbeat ticker
			hbTicker := time.NewTicker(hbInterval)
			readErr := make(chan error, 1)

			// read loop
			go func(c *websocket.Conn, re chan error) {
				defer close(re)
				for {
					_, msg, err := c.ReadMessage()
					if err != nil {
						re <- err
						return
					}
					// try pretty JSON
					var v interface{}
					if err := json.Unmarshal(msg, &v); err == nil {
						pretty, _ := json.MarshalIndent(v, "", "  ")
						log.Infof("[control] recv:\n%s", string(pretty))
					} else {
						log.Debugf("[control] recv raw: %x", msg)
					}
				}
			}(conn, readErr)

			// heartbeat + read wait
		loopControl:
			for {
				select {
				case <-ctx.Done():
					hbTicker.Stop()
					_ = conn.Close()
					return
				case <-hbTicker.C:
					h := controlHeartbeat{
						Type:      "heartbeat",
						Timestamp: time.Now().Unix(),
						WorkerId:  workerID,
					}
					if err := conn.WriteJSON(h); err != nil {
						log.Warnf("[control] heartbeat write failed: %v", err)
						_ = conn.Close()
						break loopControl
					}
					log.Debug("[control] heartbeat sent")
				case err := <-readErr:
					hbTicker.Stop()
					_ = conn.Close()
					if err != nil {
						log.Warnf("[control] read loop ended: %v", err)
					} else {
						log.Info("[control] read loop ended")
					}
					// reconnect
					break loopControl
				}
			}
			// small delay before reconnect
			time.Sleep(backoff)
		}
	}()
}

// connectData tries to connect to data channel, send WelcomeMessage on success and returns conn
func connectDataLoop(ctx context.Context, wg *sync.WaitGroup, baseURL, secret, workerID string, reconnectDelay time.Duration, log *logrus.Logger) <-chan *websocket.Conn {
	out := make(chan *websocket.Conn)
	wg.Add(1)
	go func() {
		defer wg.Done()
		backoff := reconnectDelay
		for {
			select {
			case <-ctx.Done():
				close(out)
				return
			default:
			}
			full := fmt.Sprintf("%s/", baseURL)
			conn, resp, err := dialWS(full, secret)
			if err != nil {
				log.Warnf("[data] dial %s failed: %v (resp=%v) — retrying in %s", full, err, resp, backoff)
				time.Sleep(backoff)
				if backoff < 30*time.Second {
					backoff += time.Second
				}
				continue
			}
			log.Info("[data] connected")

			w := &rotompb.WelcomeMessage{
				WorkerId:    workerID,
				Origin:      "lab",
				VersionCode: 1,
				VersionName: "rotom-worker",
				Useragent:   "rotom-worker/1.0",
				DeviceId:    workerID + "-device",
			}
			if b, err := proto.Marshal(w); err == nil {
				if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
					log.Warnf("[data] send welcome failed: %v", err)
					_ = conn.Close()
					time.Sleep(backoff)
					continue
				}
				log.Info("[data] welcome sent")
			} else {
				log.Warnf("[data] proto marshal welcome failed: %v", err)
			}
			backoff = reconnectDelay
			// deliver the connected ws to consumer
			select {
			case out <- conn:
				// consumer may take ownership; after consumer returns, we'll loop to reconnect again if needed
				// wait until caller closes conn or ctx cancelled
				// here we simply wait until ctx cancel and then continue to attempt reconnect later if needed
				// NOTE: caller is responsible for closing conn when done
				return
			case <-ctx.Done():
				_ = conn.Close()
				close(out)
				return
			}
		}
	}()
	return out
}

// sendMitm sends a MitmRequest with the file bytes using the oneof wrapper
func sendMitm(ws *websocket.Conn, id uint32, payload []byte, log *logrus.Logger) error {
	wsWriteMu.Lock()
	defer wsWriteMu.Unlock()

	msg := &rotompb.MitmRequest{
		Id:     id,
		Method: rotompb.MitmRequest_RPC_REQUEST,
		Payload: &rotompb.MitmRequest_RpcRequest_{
			RpcRequest: &rotompb.MitmRequest_RpcRequest{
				Request: []*rotompb.MitmRequest_RpcRequest_SingleRpcRequest{
					{
						Method:       1,
						Payload:      payload,
						IsCompressed: false,
					},
				},
				Lat: 0,
				Lon: 0,
			},
		},
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return err
	}
	log.Infof("[send] sent id=%d bytes=%d", id, len(payload))
	return nil
}


// scan directory and enqueue SendItems on ch
func scanDirEnqueue(ctx context.Context, ch chan<- SendItem, scanDir string, minSize int64, log *logrus.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if info.Size() < minSize {
				return nil
			}
			data, err := ioutil.ReadFile(path)
			if err != nil {
				log.Debugf("[scan] failed read %s: %v", path, err)
				return nil
			}
			item := SendItem{Path: path, Request: data}
			select {
			case ch <- item:
				log.Infof("[scan] enqueued %s (%d)", path, len(data))
			default:
				// queue full, drop or skip
				log.Debugf("[scan] queue full, skipping %s", path)
			}
			return nil
		})
		// sleep between scans
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

func senderWorker(ctx context.Context, ws *websocket.Conn, ch <-chan SendItem, log *logrus.Logger, workerIdx int) {
	idCounter := uint32(workerIdx * 1000000) // distinct id space per worker
	for {
		select {
		case <-ctx.Done():
			return
		case it, ok := <-ch:
			if !ok {
				return
			}
			if len(it.Request) == 0 {
				log.Debugf("[worker%d] empty payload for %s", workerIdx, it.Path)
				continue
			}
			// send with retry once
			if err := sendMitm(ws, idCounter, it.Request, log); err != nil {
				log.Warnf("[worker%d] send failed id=%d path=%s err=%v", workerIdx, idCounter, it.Path, err)
				// attempt a single reconnect/send (best-effort)
				_ = ws.Close()
				// caller should re-establish ws; here we break to stop worker
				return
			}
			idCounter++
			// small delay to avoid flooding
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(120) * time.Millisecond):
			}
		}
	}
}

func main() {
	flag.Parse()

	// read config
	cfg, err := readConfig(*flagConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read config %s: %v\n", *flagConfig, err)
		os.Exit(2)
	}

	log := setupLogger(cfg)
	log.Infof("rotom-worker starting; rotom=%s control=%s worker=%s", cfg.Rotom.WorkerEndpoint, cfg.Rotom.DeviceEndpoint, cfg.General.DeviceName)

	// context + signal
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// start control loop
	controlLoop(ctx, &wg, cfg.Rotom.WorkerEndpoint, cfg.Rotom.Secret, cfg.General.DeviceName, 15*time.Second, log)

	// start data connect and obtain ws connection channel
	connCh := connectDataLoop(ctx, &wg, cfg.Rotom.WorkerEndpoint, cfg.Rotom.Secret, cfg.General.DeviceName, 3*time.Second, log)

	// queue channel
	queueSize := 1024
	itemCh := make(chan SendItem, queueSize)

	// start scanner
	go scanDirEnqueue(ctx, itemCh, cfg.General.ScanDir, 512, log)

	// when we get a data connection, launch worker goroutines to consume queue using that connection
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ws, ok := <-connCh:
				if !ok {
					log.Info("[main] data connection channel closed")
					return
				}
				// for each new ws connection, spawn workers that use it
				log.Info("[main] starting sender workers for new data connection")
				var wgsync sync.WaitGroup
				workerCount := cfg.General.Workers
				for i := 0; i < workerCount; i++ {
					wgsync.Add(1)
					go func(idx int) {
						defer wgsync.Done()
						// spawn each worker with a child context so we can stop them easily if ws dies
						workerCtx, workerCancel := context.WithCancel(ctx)
						defer workerCancel()
						senderWorker(workerCtx, ws, itemCh, log, idx+1)
					}(i)
					// small stagger
					time.Sleep(time.Duration(cfg.Tuning.WorkerSpawnDelayMs) * time.Millisecond)
				}
				// wait for workers to exit (they exit if ws.Close or ctx cancelled)
				wgsync.Wait()
				// close ws if still open
				_ = ws.Close()
				log.Info("[main] sender workers finished for this data connection; attempting reconnect")
				// reconnect: get new ws from connectDataLoop (it returns only one conn in this design)
				// loop continues to wait on connCh for next connection
			}
		}
	}()

	// wait for signal
	select {
	case sig := <-sigs:
		log.Warnf("received signal %s, shutting down", sig)
		cancel()
	case <-ctx.Done():
	}

	// graceful shutdown
	cancel()
	wg.Wait()
	// drain queue (best effort)
	close(itemCh)
	time.Sleep(500 * time.Millisecond)
	log.Info("rotom-worker stopped")
}
