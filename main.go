package main

import (
	"flag"
	"fmt"
	"net/http"

	"github.com/IRONICBo/distribute-scheduler/internal/config"
	"github.com/IRONICBo/distribute-scheduler/internal/handler"
	"github.com/IRONICBo/distribute-scheduler/internal/server"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"
)

func main() {
	var webhookConfig config.WebhookConfig
	var rootCmd = &cobra.Command{
		Use: "webhook-scheduler",
	}

	klog.InitFlags(nil)
	rootCmd.Flags().IntVarP(&webhookConfig.Port, "port", "p", 8443, "Webhook server port.")
	rootCmd.Flags().StringVarP(&webhookConfig.CertFile, "cert-file", "c", "/tmp/webhook/certs/tls.crt", "File containing the x509 Certificate for HTTPS.")
	rootCmd.Flags().StringVarP(&webhookConfig.KeyFile, "key-file", "k", "/tmp/webhook/certs/tls.key", "File containing the x509 private key to --cert-file.")
	rootCmd.Flags().StringVarP(&webhookConfig.V, "v", "v", "2", "Log level.")
	if err := rootCmd.Execute(); err != nil {
		fmt.Printf("Failed to execute command: %v\n", err)
	}
	// Update default log level
	flag.Set("v", webhookConfig.V)
	defer klog.Flush()

	// Router
	stopCh := make(chan struct{})
	mux := http.NewServeMux()
	mutateHandler := handler.NewWebhookHandler(stopCh)
	mux.HandleFunc("/mutate", mutateHandler.MutateHandler)

	// Create a new webhook server
	webhookServer := server.NewWebhookServer(webhookConfig.Port, webhookConfig.CertFile, webhookConfig.KeyFile, mux)
	webhookServer.Serve()
	stopCh <- struct{}{}
}
