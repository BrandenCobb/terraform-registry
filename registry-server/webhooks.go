package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// WebhookConfig represents a configured webhook endpoint.
type WebhookConfig struct {
	URL     string   `json:"url"`               // Webhook endpoint URL
	Secret  string   `json:"secret,omitempty"`   // HMAC secret for signing payloads
	Events  []string `json:"events"`             // Events to subscribe to: publish, delete, deprecate
	Enabled bool     `json:"enabled"`
}

// WebhookPayload is the JSON body sent to webhook endpoints.
type WebhookPayload struct {
	Event     string      `json:"event"`      // publish, delete, deprecate
	Kind      string      `json:"kind"`       // provider, module
	Namespace string      `json:"namespace"`
	Name      string      `json:"name"`
	Provider  string      `json:"provider,omitempty"`
	Version   string      `json:"version"`
	Platform  string      `json:"platform,omitempty"` // os/arch for providers
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"` // Additional context
}

// WebhookManager manages webhook delivery.
type WebhookManager struct {
	webhooks []WebhookConfig
	client   *http.Client
	logger   *slog.Logger
}

// NewWebhookManager creates a webhook manager from config.
func NewWebhookManager(configPath string, logger *slog.Logger) *WebhookManager {
	wm := &WebhookManager{
		client: &http.Client{Timeout: 10 * time.Second},
		logger: logger,
	}
	if configPath != "" {
		wm.loadConfig(configPath)
	}
	return wm
}

func (wm *WebhookManager) loadConfig(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			wm.logger.Warn("failed to load webhook config", "error", err)
		}
		return
	}
	var config struct {
		Webhooks []WebhookConfig `json:"webhooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		wm.logger.Warn("invalid webhook config", "error", err)
		return
	}
	wm.webhooks = config.Webhooks
	wm.logger.Info("loaded webhooks", "count", len(wm.webhooks))
}

// Notify sends an event to all matching webhook endpoints.
func (wm *WebhookManager) Notify(event string, payload WebhookPayload) {
	if len(wm.webhooks) == 0 {
		return
	}

	payload.Event = event
	payload.Timestamp = time.Now().UTC()

	body, err := json.Marshal(payload)
	if err != nil {
		wm.logger.Error("failed to marshal webhook payload", "error", err)
		return
	}

	for _, wh := range wm.webhooks {
		if !wh.Enabled {
			continue
		}
		matched := false
		for _, e := range wh.Events {
			if e == event || e == "*" {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		go func(wh WebhookConfig) {
			req, err := http.NewRequest("POST", wh.URL, bytes.NewReader(body))
			if err != nil {
				wm.logger.Error("webhook request creation failed", "url", wh.URL, "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Registry-Event", event)
			if wh.Secret != "" {
				sig := sha256Hex(append([]byte(wh.Secret), body...))
				req.Header.Set("X-Registry-Signature", fmt.Sprintf("sha256=%s", sig))
			}

			resp, err := wm.client.Do(req)
			if err != nil {
				wm.logger.Warn("webhook delivery failed", "url", wh.URL, "error", err)
				return
			}
			_ = resp.Body.Close()

			if resp.StatusCode >= 400 {
				wm.logger.Warn("webhook returned error",
					"url", wh.URL,
					"status", resp.StatusCode,
					"event", event,
				)
			} else {
				wm.logger.Debug("webhook delivered",
					"url", wh.URL,
					"event", event,
					"status", resp.StatusCode,
				)
			}
		}(wh)
	}
}

// Reload re-reads the webhook configuration file.
func (wm *WebhookManager) Reload(path string) {
	wm.loadConfig(path)
}

// loadWebhookConfigPath returns the webhook config path from environment.
func loadWebhookConfigPath() string {
	return os.Getenv("WEBHOOK_CONFIG")
}

// shouldNotify checks if the webhook config has any matching subscribers.
func (wm *WebhookManager) shouldNotify(event string) bool {
	for _, wh := range wm.webhooks {
		if !wh.Enabled {
			continue
		}
		for _, e := range wh.Events {
			if e == event || e == "*" {
				return true
			}
		}
	}
	return false
}

// webhookConfigExample returns an example webhook configuration.
func webhookConfigExample() string {
	return `{
  "webhooks": [
    {
      "url": "https://hooks.example.com/registry",
      "secret": "optional-hmac-secret",
      "events": ["publish", "delete", "deprecate"],
      "enabled": true
    },
    {
      "url": "http://slack-bot:9090/notify",
      "events": ["publish"],
      "enabled": false
    }
  ]
}`
}

// Matches checks if a string matches a pattern (supports * wildcard).
func webhookEventMatch(events []string, event string) bool {
	for _, e := range events {
		if e == "*" || strings.EqualFold(e, event) {
			return true
		}
	}
	return false
}
