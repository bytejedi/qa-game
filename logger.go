package main

import (
	"encoding/json"
	"fmt"
	"log"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Logger *zap.Logger

func InitLogger(verbosity int) { // Logging verbosity: 0=debug, 1=error, 2=warn, 3=info
	var err error
	level := []string{
		"DEBUG",
		"ERROR",
		"WARN",
		"INFO",
	}

	log.SetFlags(log.Lmicroseconds | log.Lshortfile | log.LstdFlags)

	if verbosity >= len(level) || verbosity < 0 {
		log.Fatal("invalid verbosity: ", verbosity)
	}

	js := fmt.Sprintf(`{
  "level": "%s",
  "encoding": "json",
  "outputPaths": ["stdout"],
  "errorOutputPaths": ["stdout"]
  }`, level[verbosity])

	var cfg zap.Config
	if err := json.Unmarshal([]byte(js), &cfg); err != nil {
		panic(err)
	}
	cfg.EncoderConfig = zap.NewProductionEncoderConfig()
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	Logger, err = cfg.Build()
	if err != nil {
		log.Fatal("init logger error: ", err)
	}
}
