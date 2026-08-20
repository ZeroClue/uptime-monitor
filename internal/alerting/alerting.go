package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ZeroClue/uptime-monitor/internal/scheduler"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

type Engine struct {
	db         *storage.DB
	sched      *scheduler.Scheduler
	logger     *slog.Logger
	thresholds map[string]ThresholdConfig
	webhooks   []WebhookConfig
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

type ThresholdConfig struct {
	Warning  float64
	Critical float64
	Below    bool
}

func exceedsThreshold(value, threshold float64, below bool) bool {
	if below {
		return value <= threshold
	}
	return value >= threshold
}

type WebhookConfig struct {
	Name   string
	URL    string
	Type   string // slack, discord, pagerduty
	Secret string
}

func NewEngine(db *storage.DB, sched *scheduler.Scheduler, logger *slog.Logger) *Engine {
	return &Engine{
		db:         db,
		sched:      sched,
		logger:     logger,
		thresholds: make(map[string]ThresholdConfig),
		webhooks:   []WebhookConfig{},
		stopCh:     make(chan struct{}),
	}
}

func (e *Engine) LoadThresholds(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var cfg struct {
		Thresholds map[string]ThresholdConfig `yaml:"thresholds"`
		Webhooks   []WebhookConfig            `yaml:"webhooks"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	e.thresholds = cfg.Thresholds
	e.webhooks = cfg.Webhooks
	return nil
}

func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.evaluateAlerts(ctx)
		}
	}
}

func (e *Engine) Stop() {
	close(e.stopCh)
}

func (e *Engine) evaluateAlerts(ctx context.Context) {
	hosts, err := e.db.GetHosts()
	if err != nil {
		e.logger.Error("failed to get hosts for alert evaluation", "error", err)
		return
	}

	for _, h := range hosts {
		status := e.sched.GetHostStatus(h.ID)
		if status == nil {
			continue
		}

		// Collection failure alert (3 consecutive fails = down)
		if status.ConsecutiveFails >= 3 {
			e.fireAlert(ctx, storage.Alert{
				HostID:   h.ID,
				Type:     "collection_failure",
				Severity: "critical",
				Message:  fmt.Sprintf("Host %s is down (%d consecutive failed polls)", h.Name, status.ConsecutiveFails),
				FiredAt:  time.Now(),
			})
		}

		// Metric threshold alerts
		e.checkMetricThresholds(ctx, h.ID, h.Name)
	}
}

func (e *Engine) checkMetricThresholds(ctx context.Context, hostID int64, hostName string) {
	if len(e.thresholds) == 0 {
		return
	}

	// Get latest samples for each metric with thresholds
	for metric, threshold := range e.thresholds {
		samples, err := e.db.GetSamples(ctx, hostID, metric, time.Now().Add(-5*time.Minute), time.Now(), "1m")
		if err != nil || len(samples) == 0 {
			continue
		}

		latest := samples[len(samples)-1].Value
		below := threshold.Below

		if exceedsThreshold(latest, threshold.Critical, below) {
			e.fireAlert(ctx, storage.Alert{
				HostID:    hostID,
				Type:      "metric_threshold",
				Metric:    metric,
				Severity:  "critical",
				Message:   fmt.Sprintf("%s on %s is %.2f (critical threshold: %.2f)", metric, hostName, latest, threshold.Critical),
				Value:     latest,
				Threshold: threshold.Critical,
				FiredAt:   time.Now(),
			})
		} else if exceedsThreshold(latest, threshold.Warning, below) {
			e.fireAlert(ctx, storage.Alert{
				HostID:    hostID,
				Type:      "metric_threshold",
				Metric:    metric,
				Severity:  "warning",
				Message:   fmt.Sprintf("%s on %s is %.2f (warning threshold: %.2f)", metric, hostName, latest, threshold.Warning),
				Value:     latest,
				Threshold: threshold.Warning,
				FiredAt:   time.Now(),
			})
		}
	}
}

func (e *Engine) fireAlert(ctx context.Context, alert storage.Alert) {
	// Check if similar alert already exists and is not acknowledged
	existing, err := e.db.GetActiveAlert(ctx, alert.HostID, alert.Type, alert.Metric)
	if err == nil && existing != nil {
		// Update existing alert
		existing.Value = alert.Value
		existing.FiredAt = alert.FiredAt
		existing.Message = alert.Message
		if err := e.db.UpdateAlert(ctx, existing); err != nil {
			e.logger.Error("failed to update alert", "error", err)
		}
		return
	}

	// Insert new alert
	if err := e.db.InsertAlert(ctx, alert); err != nil {
		e.logger.Error("failed to insert alert", "error", err)
		return
	}

	// Send webhook notifications
	e.sendWebhooks(alert)
}

func (e *Engine) sendWebhooks(alert storage.Alert) {
	for _, webhook := range e.webhooks {
		go func(w WebhookConfig) {
			payload := e.buildWebhookPayload(alert, w.Type)
			if err := e.postWebhook(w.URL, payload); err != nil {
				e.logger.Error("webhook failed", "webhook", w.Name, "error", err)
			}
		}(webhook)
	}
}

func (e *Engine) buildWebhookPayload(alert storage.Alert, webhookType string) []byte {
	switch webhookType {
	case "slack":
		color := "#ff0000"
		if alert.Severity == "warning" {
			color = "#ffaa00"
		}
		return []byte(fmt.Sprintf(`{"attachments":[{"color":"%s","title":"%s","text":"%s","fields":[{"title":"Host","value":"%d","short":true},{"title":"Severity","value":"%s","short":true}]}]}`, color, alert.Message, alert.Message, alert.HostID, alert.Severity))
	case "discord":
		color := 16711680
		if alert.Severity == "warning" {
			color = 16753920
		}
		return []byte(fmt.Sprintf(`{"embeds":[{"title":"%s","description":"%s","color":%d,"fields":[{"name":"Host ID","value":"%d","inline":true},{"name":"Severity","value":"%s","inline":true}]}]}`, alert.Message, alert.Message, color, alert.HostID, alert.Severity))
	case "pagerduty":
		payload := map[string]interface{}{
			"routing_key":  "",
			"event_action": "trigger",
			"payload": map[string]interface{}{
				"summary":   alert.Message,
				"severity":  alert.Severity,
				"source":    "uptime-monitor",
				"component": fmt.Sprintf("host-%d", alert.HostID),
			},
		}
		data, _ := json.Marshal(payload)
		return data
	default:
		return []byte(fmt.Sprintf(`{"alert":%s}`, toJSON(alert)))
	}
}

func (e *Engine) postWebhook(url string, payload []byte) error {
	req, err := http.NewRequest("POST", url, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}
