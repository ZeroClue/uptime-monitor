package alerting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ZeroClue/uptime-monitor/internal/scheduler"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

type Engine struct {
	db       *storage.DB
	sched    *scheduler.Scheduler
	logger   *slog.Logger
	rules    []storage.AlertRule
	channels []storage.NotificationChannel
	config   *storage.AlertConfig
	stopCh   chan struct{}
	wg       sync.WaitGroup
	mu       sync.RWMutex
}

func NewEngine(db *storage.DB, sched *scheduler.Scheduler, logger *slog.Logger) *Engine {
	return &Engine{
		db:       db,
		sched:    sched,
		logger:   logger,
		rules:    []storage.AlertRule{},
		channels: []storage.NotificationChannel{},
		stopCh:   make(chan struct{}),
	}
}

func (e *Engine) LoadFromConfig(configPath string) error {
	// Load from YAML and seed DB if empty
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var cfg struct {
		Thresholds map[string]struct {
			Warning  float64 `yaml:"warning"`
			Critical float64 `yaml:"critical"`
			Below    bool    `yaml:"below"`
		} `yaml:"thresholds"`
		Webhooks []struct {
			Name   string `yaml:"name"`
			URL    string `yaml:"url"`
			Type   string `yaml:"type"`
			Secret string `yaml:"secret"`
		} `yaml:"webhooks"`
		Collection struct {
			FailureThreshold int `yaml:"failure_threshold"`
		} `yaml:"collection"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	// Seed alert rules if DB is empty
	existingRules, err := e.db.GetAlertRules(context.Background())
	if err != nil {
		return err
	}
	if len(existingRules) == 0 {
		for metric, t := range cfg.Thresholds {
			rule := storage.AlertRule{
				Metric:   metric,
				Scope:    "global",
				Warning:  t.Warning,
				Critical: t.Critical,
				Below:    t.Below,
				Enabled:  true,
			}
			if _, err := e.db.CreateAlertRule(context.Background(), &rule); err != nil {
				return err
			}
		}
	}

	// Seed notification channels if DB is empty
	existingChannels, err := e.db.GetNotificationChannels(context.Background())
	if err != nil {
		return err
	}
	if len(existingChannels) == 0 {
		for _, w := range cfg.Webhooks {
			config := map[string]string{
				"url":    w.URL,
				"secret": w.Secret,
			}
			configJSON, _ := json.Marshal(config)
			channel := storage.NotificationChannel{
				Name:    w.Name,
				Type:    w.Type,
				Config:  string(configJSON),
				Enabled: true,
			}
			if _, err := e.db.CreateNotificationChannel(context.Background(), &channel); err != nil {
				return err
			}
		}
	}

	// Seed alert config if DB is empty
	existingConfig, err := e.db.GetAlertConfig(context.Background())
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if existingConfig == nil {
		failureThreshold := cfg.Collection.FailureThreshold
		if failureThreshold <= 0 {
			failureThreshold = 3
		}
		webhookConfigs := make([]map[string]string, len(cfg.Webhooks))
		for i, w := range cfg.Webhooks {
			webhookConfigs[i] = map[string]string{
				"url":    w.URL,
				"secret": w.Secret,
			}
		}
		webhooksJSON, _ := json.Marshal(webhookConfigs)
		config := storage.AlertConfig{
			CollectionFailureThreshold: failureThreshold,
			Webhooks:                   string(webhooksJSON),
		}
		if _, err := e.db.CreateAlertConfig(context.Background(), &config); err != nil {
			return err
		}
	}

	return e.refreshFromDB()
}

func (e *Engine) refreshFromDB() error {
	rules, err := e.db.GetAlertRules(context.Background())
	if err != nil {
		return err
	}
	channels, err := e.db.GetEnabledNotificationChannels(context.Background())
	if err != nil {
		return err
	}
	config, err := e.db.GetAlertConfig(context.Background())
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.rules = rules
	e.channels = channels
	e.config = config
	e.mu.Unlock()
	return nil
}

func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Initial refresh
	e.refreshFromDB()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.refreshFromDB()
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

	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	for _, h := range hosts {
		status := e.sched.GetHostStatus(h.ID)
		if status == nil {
			continue
		}

		// Collection failure alert (configurable consecutive fails = down)
		e.mu.RLock()
		threshold := e.config.CollectionFailureThreshold
		e.mu.RUnlock()
		if threshold <= 0 {
			threshold = 3
		}
		if status.ConsecutiveFails >= threshold {
			e.fireAlert(ctx, h.ProjectID, storage.Alert{
				HostID:   h.ID,
				Type:     "collection_failure",
				Severity: "critical",
				Message:  fmt.Sprintf("Host %s is down (%d consecutive failed polls)", h.Name, status.ConsecutiveFails),
				FiredAt:  time.Now(),
			})
		}

		// Metric threshold alerts
		e.checkMetricThresholds(ctx, h.ID, h.Name, h.ProjectID, rules)
	}
}

// ruleMatchesProject: a rule applies to a host when the rule is global
// (nil project) or scoped to the host's own project. Hosts without a
// project only match global rules.
func ruleMatchesProject(rule storage.AlertRule, hostProject *int64) bool {
	if rule.ProjectID == nil {
		return true
	}
	return hostProject != nil && *rule.ProjectID == *hostProject
}

func (e *Engine) checkMetricThresholds(ctx context.Context, hostID int64, hostName string, hostProject *int64, rules []storage.AlertRule) {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		// Check if rule applies to this host
		if rule.Scope == "host" && rule.HostID != nil && *rule.HostID != hostID {
			continue
		}
		if !ruleMatchesProject(rule, hostProject) {
			continue
		}

		samples, err := e.db.GetSamples(ctx, hostID, rule.Metric, time.Now().Add(-5*time.Minute), time.Now(), "1m")
		if err != nil || len(samples) == 0 {
			continue
		}

		latest := samples[len(samples)-1].Value
		below := rule.Below

		if exceedsThreshold(latest, rule.Critical, below) {
			e.fireAlert(ctx, hostProject, storage.Alert{
				HostID:    hostID,
				Type:      "metric_threshold",
				Metric:    rule.Metric,
				Severity:  "critical",
				Message:   fmt.Sprintf("%s on %s is %.2f (critical threshold: %.2f)", rule.Metric, hostName, latest, rule.Critical),
				Value:     latest,
				Threshold: rule.Critical,
				FiredAt:   time.Now(),
			})
		} else if exceedsThreshold(latest, rule.Warning, below) {
			e.fireAlert(ctx, hostProject, storage.Alert{
				HostID:    hostID,
				Type:      "metric_threshold",
				Metric:    rule.Metric,
				Severity:  "warning",
				Message:   fmt.Sprintf("%s on %s is %.2f (warning threshold: %.2f)", rule.Metric, hostName, latest, rule.Warning),
				Value:     latest,
				Threshold: rule.Warning,
				FiredAt:   time.Now(),
			})
		}
	}
}

func (e *Engine) fireAlert(ctx context.Context, hostProject *int64, alert storage.Alert) {
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

	// Send notifications via enabled channels, filtered by the alerting
	// host's project (global channels receive everything).
	e.mu.RLock()
	chans := e.channels // refreshed periodically; a channel re-scoping takes up to one refresh cycle to apply
	e.mu.RUnlock()
	e.sendNotifications(alert, channelsForProject(chans, hostProject))
}

func (e *Engine) sendNotifications(alert storage.Alert, channels []storage.NotificationChannel) {
	for _, ch := range channels {
		go func(ch storage.NotificationChannel) {
			var config map[string]string
			_ = json.Unmarshal([]byte(ch.Config), &config)
			if ch.Type == "email" {
				if err := e.sendEmail(alert, config); err != nil {
					e.logger.Error("notification failed", "channel", ch.Name, "type", ch.Type, "error", err)
				}
			} else {
				payload := e.buildPayload(alert, ch.Type, config)
				if err := e.postWebhook(config["url"], payload); err != nil {
					e.logger.Error("notification failed", "channel", ch.Name, "type", ch.Type, "error", err)
				}
			}
		}(ch)
	}
}

func (e *Engine) buildPayload(alert storage.Alert, channelType string, config map[string]string) []byte {
	switch channelType {
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

func (e *Engine) sendEmail(alert storage.Alert, config map[string]string) error {
	host := config["smtp_host"]
	port := config["smtp_port"]
	username := config["username"]
	password := config["password"]
	from := config["from"]
	to := config["to"]
	if host == "" || port == "" || username == "" || password == "" || from == "" || to == "" {
		return fmt.Errorf("missing required email config fields")
	}

	subject := fmt.Sprintf("[%s] %s", strings.ToUpper(alert.Severity), alert.Message)
	body := fmt.Sprintf(`%s

Host: %d
Metric: %s
Value: %.2f
Threshold: %.2f
Fired: %s`,
		alert.Message,
		alert.HostID,
		alert.Metric,
		alert.Value,
		alert.Threshold,
		alert.FiredAt.Format(time.RFC3339))

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", from, to, subject, body)

	auth := smtp.PlainAuth("", username, password, host)
	addr := fmt.Sprintf("%s:%s", host, port)
	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg))
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

func exceedsThreshold(value, threshold float64, below bool) bool {
	if below {
		return value <= threshold
	}
	return value >= threshold
}

// channelsForProject: a channel receives an alert when it is global (nil
// project) or scoped to the alerting host's own project. Unassigned-host
// alerts go to global channels only. Mirrors ruleMatchesProject semantics.
func channelsForProject(channels []storage.NotificationChannel, hostProject *int64) []storage.NotificationChannel {
	out := make([]storage.NotificationChannel, 0, len(channels))
	for _, ch := range channels {
		if ch.ProjectID == nil {
			out = append(out, ch)
		} else if hostProject != nil && *ch.ProjectID == *hostProject {
			out = append(out, ch)
		}
	}
	return out
}
