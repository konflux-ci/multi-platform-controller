package mpclogs

import (
	"flag"
	zap2 "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const DebugLevel = 1

func InitLogger(logLevel, stackTraceLevel string) error {

	zapFlagSet := flag.NewFlagSet("zap", flag.ContinueOnError)
	opts := zap.Options{
		TimeEncoder: zapcore.RFC3339TimeEncoder,
		ZapOpts:     []zap2.Option{zap2.WithCaller(true)},
	}
	opts.BindFlags(zapFlagSet)
	klog.InitFlags(zapFlagSet)

	if logLevel != "" {
		if err := zapFlagSet.Set("zap-log-level", logLevel); err != nil {
			return err
		}
	}
	if stackTraceLevel != "" {
		if err := zapFlagSet.Set("zap-stacktrace-level", stackTraceLevel); err != nil {
			return err
		}
	}

	logger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(logger)
	klog.SetLoggerWithOptions(logger, klog.ContextualLogger(true))
	return nil
}
