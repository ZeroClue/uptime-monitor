package storage

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
	logger *slog.Logger
}

func New(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "monitor.db")
	sqlDB, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	// Ensure database file is created
	if err := sqlDB.Ping(); err != nil {
		return nil, err
	}
	return &DB{DB: sqlDB, logger: slog.Default()}, nil
}

func (db *DB) DBSizeMB() float64 {
	var size int64
	err := db.QueryRow(`SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()`).Scan(&size)
	if err != nil {
		return 0
	}
	return float64(size) / (1024 * 1024)
}

type Host struct {
	ID                  int64
	Name                string
	Connection          string
	Endpoint            string
	Port                int
	User                string
	KeyPath             string
	Sudo                bool
	TimeoutRaw          int64
	Timeout             time.Duration
	ProxyJump           string
	Tags                []string
	CollectorPreference string
}

type Alert struct {
	ID             int64
	HostID         int64
	Type           string
	Metric         string
	Severity       string
	Message        string
	Value          float64
	Threshold      float64
	FiredAt        time.Time
	AcknowledgedAt *time.Time
	ResolvedAt     *time.Time
	SilencedUntil  *time.Time
}

type AlertRule struct {
	ID        int64
	Metric    string
	Scope     string // "global" or "host"
	HostID    *int64
	Warning   float64
	Critical  float64
	Below     bool
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type NotificationChannel struct {
	ID        int64
	Name      string
	Type      string // "slack", "discord", "pagerduty", "webhook", "email"
	Config    string // JSON config
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AlertConfig struct {
	ID                         int64
	CollectionFailureThreshold int
	Webhooks                   string // JSON array of webhook configs
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

type Project struct {
	ID              int64
	Name            string
	Type            string // "tag_query" or "explicit"
	TagQuery        string
	HostIDs         []int64
	OwnerID         *int64
	IsolationLevel  string
	IsDefault       bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Email        string
	Role         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ProjectMember struct {
	ID        int64
	ProjectID int64
	UserID    int64
	Role      string
	CreatedAt time.Time
}

type Sample struct {
	HostID    int64
	Metric    string
	Value     float64
	Timestamp time.Time
	Collector string
}
