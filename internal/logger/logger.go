package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type Logger struct {
	eventFile io.WriteCloser
	mutex     sync.Mutex
	debug     bool
}

func Start(logPath string, logLevel string) (*Logger, error) {
	logFile := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    10,
		MaxBackups: 5,
		MaxAge:     7,
		Compress:   true,
	}

	return &Logger{
		eventFile: logFile,
		debug:     logLevel == "debug",
	}, nil
}

func (l *Logger) Info(eventType string, data map[string]interface{}) {
	l.log("INFO", eventType, data)
}

func (l *Logger) Error(eventType string, data map[string]interface{}) {
	l.log("ERROR", eventType, data)
}

// Debug logs only when the logger was started with logLevel "debug".
func (l *Logger) Debug(eventType string, data map[string]interface{}) {
	if l.debug {
		l.log("DEBUG", eventType, data)
	}
}

func (l *Logger) log(level, eventType string, data map[string]interface{}) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	event := map[string]interface{}{
		"time":  time.Now().Format(time.RFC3339),
		"level": level,
		"event": eventType,
		"data":  data,
	}

	json.NewEncoder(l.eventFile).Encode(event)
	fmt.Printf("[%s] %s: %v\n", level, eventType, data)
}

func (l *Logger) Close() error {
	return l.eventFile.Close()
}
