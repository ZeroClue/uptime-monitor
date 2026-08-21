package storage

import (
	"context"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/config"
)

// HostStore defines the interface for host persistence.
type HostStore interface {
	GetHosts() ([]Host, error)
	SeedHosts([]config.Host) error
}

// SampleStore defines the interface for metric sample persistence.
type SampleStore interface {
	SaveSamples([]collector.Sample) error
	GetSamples(ctx context.Context, hostID int64, metric string, from, to time.Time, resolution string) ([]Sample, error)
}

// AlertStore defines the interface for alert persistence.
type AlertStore interface {
	InsertAlert(ctx context.Context, alert Alert) error
	GetActiveAlert(ctx context.Context, hostID int64, alertType, metric string) (*Alert, error)
	UpdateAlert(ctx context.Context, alert *Alert) error
	AcknowledgeAlert(ctx context.Context, alertID int64) error
	SilenceAlert(ctx context.Context, alertID int64, duration time.Duration) error
	GetAlerts(ctx context.Context, hostID int64) ([]Alert, error)
	GetAllAlerts(ctx context.Context) ([]AlertWithHost, error)
}

// ProjectStore defines the interface for project persistence.
type ProjectStore interface {
	GetProjects(ctx context.Context) ([]Project, error)
	GetProject(ctx context.Context, id int64) (*Project, error)
	CreateProject(ctx context.Context, project *Project) (int64, error)
	UpdateProject(ctx context.Context, project *Project) error
	DeleteProject(ctx context.Context, id int64) error
	GetProjectHosts(ctx context.Context, project Project) ([]Host, error)
	GetProjectHealth(ctx context.Context, project Project, hostStatuses map[int64]HostStatusInfo) (string, error)
}

// Downsampler defines the interface for metric downsampling.
type Downsampler interface {
	Downsample(ctx context.Context) error
}

// Cleanup defines the interface for data cleanup.
type Cleanup interface {
	Cleanup(ctx context.Context) error
}

// Migrator defines the interface for schema migrations.
type Migrator interface {
	Migrate() error
}

// AlertRuleStore defines the interface for alert rule persistence.
type AlertRuleStore interface {
	GetAlertRules(ctx context.Context) ([]AlertRule, error)
	GetAlertRule(ctx context.Context, id int64) (*AlertRule, error)
	CreateAlertRule(ctx context.Context, rule *AlertRule) (int64, error)
	UpdateAlertRule(ctx context.Context, rule *AlertRule) error
	DeleteAlertRule(ctx context.Context, id int64) error
	GetAlertRulesForMetric(ctx context.Context, metric string) ([]AlertRule, error)
}

// NotificationChannelStore defines the interface for notification channel persistence.
type NotificationChannelStore interface {
	GetNotificationChannels(ctx context.Context) ([]NotificationChannel, error)
	GetNotificationChannel(ctx context.Context, id int64) (*NotificationChannel, error)
	CreateNotificationChannel(ctx context.Context, channel *NotificationChannel) (int64, error)
	UpdateNotificationChannel(ctx context.Context, channel *NotificationChannel) error
	DeleteNotificationChannel(ctx context.Context, id int64) error
	GetEnabledNotificationChannels(ctx context.Context) ([]NotificationChannel, error)
}

// APITokenStore defines the interface for API token persistence.
type APITokenStore interface {
	GetAPITokens(ctx context.Context) ([]APIToken, error)
	GetAPIToken(ctx context.Context, id int64) (*APIToken, error)
	GetAPITokenByName(ctx context.Context, name string) (*APIToken, error)
	CreateAPIToken(ctx context.Context, token *APIToken) (string, int64, error)
	UpdateAPIToken(ctx context.Context, token *APIToken) error
	DeleteAPIToken(ctx context.Context, id int64) error
	RecordTokenUsage(ctx context.Context, id int64) error
}

// UserStore defines the interface for user persistence.
type UserStore interface {
	GetUser(ctx context.Context, id int64) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	CreateUser(ctx context.Context, user *User) (int64, error)
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, id int64) error
	GetUsers(ctx context.Context) ([]User, error)
}

// ProjectMemberStore defines the interface for project member persistence.
type ProjectMemberStore interface {
	GetProjectMembers(ctx context.Context, projectID int64) ([]ProjectMember, error)
	AddProjectMember(ctx context.Context, projectID, userID int64, role string) (int64, error)
	RemoveProjectMember(ctx context.Context, projectID, userID int64) error
	UpdateProjectMemberRole(ctx context.Context, projectID, userID int64, role string) error
	IsProjectMember(ctx context.Context, projectID, userID int64) (bool, error)
}
