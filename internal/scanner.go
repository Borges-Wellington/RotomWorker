package internal

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

func ScannerLoop(ctxCtx context.Context, scanDir string) {
    logger := NewLogger()
    if scanDir == "" {
        scanDir = "/data/local/tmp/rotom_inbox"
    }
    if _, err := os.Stat(scanDir); os.IsNotExist(err) {
        _ = os.MkdirAll(scanDir, 0755)
    }

    ticker := time.NewTicker(3 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctxCtx.Done():
            logger.Info("[scanner] exiting")
            return
        case <-ticker.C:
            files, err := ioutil.ReadDir(scanDir)
            if err != nil {
                logger.Errorf("[scanner] readdir: %v", err)
                continue
            }
            for _, f := range files {
                if !f.Mode().IsRegular() {
                    continue
                }
                if f.Size() < 16 {
                    continue
                }
                path := filepath.Join(scanDir, f.Name())
                b, err := ioutil.ReadFile(path)
                if err != nil {
                    logger.Errorf("[scanner] read %s: %v", path, err)
                    continue
                }
                SendQueue <- SendItem{Path: path, Payload: b}
                logger.Infof("[scanner] enqueued %s", path)
            }
        }
    }
}
