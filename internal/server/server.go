package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/klog/v2"
)

type WebhookServer struct {
	certFile string
	keyFile  string
	Server   *http.Server
}

func NewWebhookServer(port int, certFile string, keyFile string, mux *http.ServeMux) *WebhookServer {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		klog.V(0).ErrorS(err, "Failed to load key pair")
		panic(err)
	}

	return &WebhookServer{
		certFile: certFile,
		keyFile:  keyFile,
		Server: &http.Server{
			Addr: fmt.Sprintf(":%d", port),
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{pair},
			},
			Handler: mux,
		},
	}
}

func (s *WebhookServer) serve() error {
	return s.Server.ListenAndServeTLS(s.certFile, s.keyFile)
}

func (s *WebhookServer) Serve() {
	go func() {
		if err := s.serve(); err != nil {
			klog.V(0).ErrorS(err, "Failed to serve")
			panic(err)
		}
	}()

	klog.V(1).InfoS("Webhook server started", "port", s.Server.Addr)

	// TODO: Graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	klog.V(1).InfoS("Shutting down webhook server")
	if err := s.Server.Shutdown(context.TODO()); err != nil {
		klog.V(0).ErrorS(err, "Failed to shutdown")
		panic(err)
	}
}
