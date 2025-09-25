// https-server.go
package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	zap2 "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	mainLog logr.Logger
)

const (
	CertFilePath = "/tls/tls.crt"
	KeyFilePath  = "/tls/tls.key"
)

func main() {
	klog.InitFlags(flag.CommandLine)

	flag.Parse()
	opts := zap.Options{
		TimeEncoder: zapcore.RFC3339TimeEncoder,
		ZapOpts:     []zap2.Option{zap2.WithCaller(true)},
	}
	logger := zap.New(zap.UseFlagOptions(&opts))

	mainLog = logger.WithName("main")
	klog.SetLogger(mainLog)
	// load tls certificates
	serverTLSCert, err := tls.LoadX509KeyPair(CertFilePath, KeyFilePath)
	if err != nil {
		log.Fatalf("Error loading certificate and key file: %v", err)
	}
	otp := NewOtp(&logger)
	store := NewStoreKey(&logger)
	mux := http.NewServeMux()
	mux.Handle("/store-key", store)
	mux.Handle("/otp", otp)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		MinVersion:   tls.VersionTLS12,
	}
	logger.Info("starting HTTP server")
	server := http.Server{
		Addr:              ":8443",
		Handler:           mux,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: time.Second * 3,
	}
	defer func() {
		if err := server.Close(); err != nil {
			logger.Error(err, "failed to shutdown server")
		}
	}()
	log.Fatal(server.ListenAndServeTLS("", ""))
}
