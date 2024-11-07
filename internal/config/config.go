package config

const (
	CapacityLabel                    = "node.kubernetes.io/capacity"
	OnDemandNodeType                 = "on-demand"
	SpotNodeType                     = "spot"
	OnDemandDeletionCost             = "100"
	SpotDeletionCost                 = "1"
	WebhookSchedulerLabel            = "cloudpilot.ai/webhook-scheduler"
	WebhookSchedulerMaxOnDemandCount = "cloudpilot.ai/webhook-scheduler-max-on-demand-count"
)

// WebhookConfig is the configuration for the webhook server.
type WebhookConfig struct {
	Port     int    `json:"port"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	// Log level
	V string `json:"v"`
}
