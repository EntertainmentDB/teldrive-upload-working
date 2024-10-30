package logger

import (
	"io"
	"path/filepath"
	"time"
	"uploader/config"
	"uploader/pkg/pb"
	"uploader/pkg/utils"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type ProgressWriterAdapter struct {
	Progress *pb.Progress
}

// func (pwa *ProgressWriterAdapter) Write(p []byte) (n int, err error) {
// 	message := string(p)
// 	pwa.Progress.WriteToProgress(message)
// 	return len(p), nil
// }

func InitLogger(options ...LoggerOption) *zap.Logger {
	customTimeEncoder := func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format("02/01/2006 03:04:00.000 PM"))
	}
	var (
		consoleConfig zapcore.EncoderConfig
		logLevel      zapcore.Level
	)

	if config.GetConfig().Debug {
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

	logPath := filepath.Join(utils.ExecutableDir(), "logs", "uploader.log")

	fileWriter := zapcore.AddSync(&lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    10,
		MaxBackups: 3,
		MaxAge:     7,
		Compress:   true,
	})

	var writers []zapcore.Core

	for _, o := range options {
		w := o()
		consoleZapCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(w), logLevel)
		writers = append(writers, consoleZapCore)
	}

	fileZapCore := zapcore.NewCore(fileEncoder, fileWriter, logLevel)
	writers = append(writers, fileZapCore)

	core := zapcore.NewTee(
		writers...,
	// zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), logLevel),
	)

	return zap.New(core, zap.AddStacktrace(zapcore.FatalLevel))
}

type LoggerOption func() io.Writer

func AddCustomWriter(w io.Writer) LoggerOption {
	return func() io.Writer {
		return w
	}
}
