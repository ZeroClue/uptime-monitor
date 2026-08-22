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
	ID                  int64         `json:"id"`
	Name                string        `json:"name"`
	Connection          string        `json:"connection"`
	Endpoint            string        `json:"endpoint"`
	Port                int           `json:"port"`
	User                string        `json:"user"`
	KeyPath             string        `json:"key_path"`
	Sudo                bool          `json:"sudo"`
	TimeoutRaw          int64         `json:"timeout"`
	Timeout             time.Duration `json:"-"`
	ProxyJump           string        `json:"proxy_jump"`
	Tags                []string      `json:"tags"`
	CollectorPreference string        `json:"collector_preference"`
	RetryMaxRetries     *int64        `json:"retry_max_retries"`
	RetryBaseMs         *int64        `json:"retry_base_delay_ms"`
	RetryMaxMs          *int64        `json:"retry_max_delay_ms"`
	SshTimeoutMs        *int64        `json:"ssh_timeout_ms"`       // connection phase; default 10s
	CollectorTimeoutMs  *int64        `json:"collector_timeout_ms"` // whole-collect budget; default 30s
	ProjectID           *int64        `json:"project_id"`
	SSHHostKeyPolicy    *string       `json:"ssh_host_key_policy"` // nil = inherit global (auto|strict|known)
	ScriptName          string        `json:"script_name"`         // custom collector: namespace custom.<script_name>.*
	ScriptCommand       string        `json:"script_command"`      // custom collector: command template over {{.Host}}/{{.Port}}
	ScriptParse         string        `json:"script_parse"`        // custom collector: json (default) | csv | plain
	SNMPVersion         string        `json:"snmp_version"`        // snmp connection: "2c" | "3"
	SNMPCommunity       string        `json:"snmp_community"`      // snmp v2c community
	SNMPv3User          string        `json:"snmp_v3_user"`
	SNMPv3AuthProto     string        `json:"snmp_v3_auth_proto"` // MD5 | SHA | SHA224 | SHA256 | SHA384 | SHA512
	SNMPv3AuthPass      string        `json:"snmp_v3_auth_pass"`
	SNMPv3PrivProto     string        `json:"snmp_v3_priv_proto"` // DES | AES | AES192 | AES256
	SNMPv3PrivPass      string        `json:"snmp_v3_priv_pass"`
	SNMPExtraOIDs       string        `json:"snmp_extra_oids"` // lines of "<oid> <metric_name>"
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
	ID        int64     `json:"id"`
	Metric    string    `json:"metric"`
	Scope     string    `json:"scope"` // "global" or "host"
	HostID    *int64    `json:"host_id"`
	ProjectID *int64    `json:"project_id"` // nil = applies to all projects
	Warning   float64   `json:"warning"`
	Critical  float64   `json:"critical"`
	Below     bool      `json:"below"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type NotificationChannel struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // "slack", "discord", "pagerduty", "webhook", "email"
	Config    string    `json:"config"`
	ProjectID *int64    `json:"project_id"` // nil = all projects
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AlertConfig struct {
	ID                         int64
	CollectionFailureThreshold int
	Webhooks                   string // JSON array of webhook configs
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

type Project struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Type           string    `json:"type"` // "tag_query" or "explicit"
	TagQuery       string    `json:"tag_query"`
	HostIDs        []int64   `json:"host_ids"`
	OwnerID        *int64    `json:"owner_id"`
	IsolationLevel string    `json:"isolation_level"`
	IsDefault      bool      `json:"is_default"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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

type APIToken struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	TokenHash  string       `json:"-"`
	ProjectID  *int64       `json:"project_id"`
	Scopes     string       `json:"scopes"`
	ExpiresAt  sql.NullTime `json:"expires_at"`
	LastUsedAt sql.NullTime `json:"last_used_at"`
	CreatedAt  sql.NullTime `json:"created_at"`
	UpdatedAt  sql.NullTime `json:"updated_at"`
}

type Sample struct {
	HostID    int64
	Metric    string
	Value     float64
	Timestamp time.Time
	Collector string
}
