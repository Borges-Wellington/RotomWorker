package main

import (
	"context"
	"fmt"        // 👈 Adicionado aqui
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"rotomworker/internal"
)

func main() {
	log := internal.NewLogger()

	cfgPath := "/data/local/tmp/rotom-config.json"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg := internal.ReadConfig(cfgPath)
	log.Infof("rotom-worker (Go hybrid) starting; rotom=%s scanDir=%s", cfg.Rotom.WorkerEndpoint, cfg.General.ScanDir)

	// Carrega hooks ELF (modo Cosmog)
	hookPaths := []string{
		"/data/local/tmp/lib/libart.so",
		"/data/local/tmp/lib/libNianticLabsPlugin.so",
	}
	if err := internal.LoadElfHooks(hookPaths); err != nil {
		fmt.Printf("Erro ao carregar hooks ELF: %v\n", err)
	}

	// initialize hooks subsystem (uses cgo + dlopen)
	libs := os.Getenv("ROTOM_LIBS") // colon separated paths, or use defaults
	if libs == "" {
		// defaults (you can set ROTOM_LIBS env variable)
		libs = "/data/local/tmp/lib/libart.so:/data/local/tmp/lib/libNianticLabsPlugin.so"
	}
	paths := strings.Split(libs, ":")
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if err := internal.LoadHookLib(p); err != nil {
			log.Errorf("failed load hook lib %s: %v", p, err)
		}
	}

	// start WS control loop (as goroutine)
	ctx, cancel := context.WithCancel(context.Background())
	go internal.ControlLoop(ctx, cfg)

	// start data websocket
	go internal.StartDataWs(ctx, cfg)

	// start scanner
	go internal.ScannerLoop(ctx, cfg.General.ScanDir)

	// start sender workers
	workerCount := cfg.General.Workers
	if workerCount < 1 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		go internal.SenderWorker(ctx, i+1)
		time.Sleep(time.Duration(cfg.Tuning.WorkerSpawnDelayMs) * time.Millisecond)
	}

	// handle signals
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Info("shutdown signal received")
	cancel()
	// give goroutines time to stop gracefully
	time.Sleep(800 * time.Millisecond)
	log.Info("rotom-worker stopped")
}
