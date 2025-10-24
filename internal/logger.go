package internal

import (
    "os"

    "github.com/sirupsen/logrus"
)

func NewLogger() *logrus.Logger {
    l := logrus.New()
    l.SetOutput(os.Stdout)
    l.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
    l.SetLevel(logrus.InfoLevel)
    if os.Getenv("DEBUG") == "true" {
        l.SetLevel(logrus.DebugLevel)
    }
    return l
}
