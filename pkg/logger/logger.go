package logger

import (
	"os"
	"time"
	"uploader/pkg/progress"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type ProgressWriterAdapter struct {
	Progress *progress.Progress
}

// func (pwa *ProgressWriterAdapter) Write(p []byte) (n int, err error) {
// 	message := string(p)
// 	pwa.Progress.WriteToProgress(message)
// 	return len(p), nil
// }

func InitLogger() *zap.Logger {
	customTimeEncoder := func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format("02/01/2006 03:04 PM"))
	}
	var (
		consoleConfig zapcore.EncoderConfig
		logLevel      zapcore.Level
	)

	dev := false

	if dev {
		consoleConfig = zap.NewDevelopmentEncoderConfig()
		logLevel = zap.DebugLevel
	} else {

		consoleConfig = zap.NewProductionEncoderConfig()
		logLevel = zap.InfoLevel
	}
	consoleConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	consoleConfig.EncodeTime = customTimeEncoder
	consoleEncoder := zapcore.NewConsoleEncoder(consoleConfig)

	fileEncoderConfig := zap.NewProductionEncoderConfig()
	fileEncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	fileEncoder := zapcore.NewJSONEncoder(fileEncoderConfig)

	fileWriter := zapcore.AddSync(&lumberjack.Logger{
		Filename:   "logs/uploader.log",
		MaxSize:    10,
		MaxBackups: 3,
		MaxAge:     7,
		Compress:   true,
	})

	core := zapcore.NewTee(
		zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), logLevel),
		zapcore.NewCore(fileEncoder, fileWriter, zapcore.DebugLevel),
		// zapcore.NewCore(zapcore.NewConsoleEncoder(consoleConfig), zapcore.AddSync(progressWriterAdapter), logLevel),
	)

	return zap.New(core, zap.AddStacktrace(zapcore.FatalLevel))
}
