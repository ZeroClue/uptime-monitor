package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

// Scope levels; higher implies lower (admin ⊃ write ⊃ read).
const (
	scopeRead = iota + 1
	scopeWrite
	scopeAdmin
)

var scopeLevels = map[string]int{
	"read":  scopeRead,
	"write": scopeWrite,
	"admin": scopeAdmin,
}

// configPrefixes are API resources whose management requires admin scope.
var configPrefixes = []string{
	"/api/alert-rules",
	"/api/notification-channels",
	"/api/api-tokens",
}

// APITokenInfo is the authenticated token identity attached to request context.
type APITokenInfo struct {
	ID        int64
	Name      string
	Scopes    string
	ProjectID *int64
}

// APITokenFromContext returns the authenticated token info, or nil when the
// request authenticated via session cookie.
func APITokenFromContext(ctx context.Context) *APITokenInfo {
	if tok, ok := ctx.Value(apiTokenCtxKey).(*APITokenInfo); ok {
		return tok
	}
	return nil
}

// requiredScope maps method+path to the minimum scope level.
func requiredScope(r *http.Request) int {
	for _, prefix := range configPrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return scopeAdmin
		}
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return scopeRead
	default:
		return scopeWrite
	}
}

func hasScope(scopes string, required int) bool {
	level, ok := scopeLevels[strings.ToLower(strings.TrimSpace(scopes))]
	return ok && level >= required
}

var (
	errBadToken = errors.New("invalid token")
	errExpired  = errors.New("token expired")
)

// authenticateToken validates the Authorization: Bearer header.
// Returns (nil, nil) when no Bearer header is present.
func (s *Server) authenticateToken(r *http.Request) (*storage.APIToken, error) {
	const prefix = "Bearer "
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, prefix) {
		return nil, nil
	}
	presented := strings.TrimSpace(authz[len(prefix):])
	if presented == "" {
		return nil, errBadToken
	}

	tok, err := s.db.GetAPITokenByHash(r.Context(), storage.HashAPIToken(presented))
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, errBadToken
	}
	if tok.ExpiresAt.Valid && !tok.ExpiresAt.Time.After(time.Now()) {
		return nil, errExpired
	}
	return tok, nil
}

// tokenRateLimiter is a fixed-window per-token limiter. Window entries are
// capped; the map resets if token churn exceeds maxTrackedWindows.
type tokenRateLimiter struct {
	mu     sync.Mutex
	wins   map[int64]*rateWindow
	limit  int
	window time.Duration
}

const maxTrackedWindows = 10_000

type rateWindow struct {
	start time.Time
	count int
}

func newTokenRateLimiter(limit int, window time.Duration) *tokenRateLimiter {
	return &tokenRateLimiter{
		wins:   make(map[int64]*rateWindow),
		limit:  limit,
		window: window,
	}
}

func (l *tokenRateLimiter) Allow(id int64) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.wins) >= maxTrackedWindows {
		l.wins = make(map[int64]*rateWindow)
	}
	w := l.wins[id]
	if w == nil || now.Sub(w.start) >= l.window {
		l.wins[id] = &rateWindow{start: now, count: 1}
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}

// RetryAfter reports the current window length in whole seconds.
func (l *tokenRateLimiter) RetryAfter() int {
	return int(l.window.Seconds())
}

// lastUsedRecorder throttles DB writes of last_used_at to once per interval.
type lastUsedRecorder struct {
	mu       sync.Mutex
	last     map[int64]time.Time
	interval time.Duration
}

func newLastUsedRecorder(interval time.Duration) *lastUsedRecorder {
	return &lastUsedRecorder{last: make(map[int64]time.Time), interval: interval}
}

func (r *lastUsedRecorder) ShouldRecord(id int64, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.last) >= maxTrackedWindows {
		r.last = make(map[int64]time.Time)
	}
	if prev, ok := r.last[id]; ok && now.Sub(prev) < r.interval {
		return false
	}
	r.last[id] = now
	return true
}

// enforceTokenAuth authenticates Bearer-token requests inside authMiddleware.
// Returns handled=true when the request carried a Bearer header (allowed or
// rejected); false means no Bearer header and cookie auth should proceed.
func (s *Server) enforceTokenAuth(next http.HandlerFunc, w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}

	tok, err := s.authenticateToken(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid or expired token")
		return true
	}
	if !s.tokenLimiter.Allow(tok.ID) {
		w.Header().Set("Retry-After", strconv.Itoa(s.tokenLimiter.RetryAfter()))
		writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return true
	}
	if !hasScope(tok.Scopes, requiredScope(r)) {
		writeJSONError(w, http.StatusForbidden, "insufficient scope")
		return true
	}
	if s.lastUsed.ShouldRecord(tok.ID, time.Now()) {
		if err := s.db.RecordTokenUsage(r.Context(), tok.ID); err != nil {
			s.logger.Debug("failed to record token usage", "token_id", tok.ID, "error", err)
		}
	}

	ctx := context.WithValue(r.Context(), apiTokenCtxKey, &APITokenInfo{
		ID:        tok.ID,
		Name:      tok.Name,
		Scopes:    tok.Scopes,
		ProjectID: tok.ProjectID,
	})
	if tok.ProjectID != nil && projectIDFromContext(ctx) == nil {
		ctx = context.WithValue(ctx, projectIDKey, tok.ProjectID)
	}
	next(w, r.WithContext(ctx))
	return true
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}

// apiTokenRequest is the wire format for token create/update; expires_at is
// a YYYY-MM-DD date string (end of day UTC).
type apiTokenRequest struct {
	Name      string  `json:"name"`
	Scopes    string  `json:"scopes"`
	ProjectID *int64  `json:"project_id"`
	ExpiresAt *string `json:"expires_at"`
}

func (req *apiTokenRequest) toAPIToken() (*storage.APIToken, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	scopes := strings.ToLower(strings.TrimSpace(req.Scopes))
	if scopes == "" {
		scopes = "read"
	}
	if _, ok := scopeLevels[scopes]; !ok {
		return nil, fmt.Errorf("invalid scopes %q (want read, write, or admin)", req.Scopes)
	}
	tok := &storage.APIToken{Name: name, Scopes: scopes, ProjectID: req.ProjectID}
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		day, err := time.Parse("2006-01-02", *req.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("invalid expires_at (want YYYY-MM-DD): %w", err)
		}
		tok.ExpiresAt = sql.NullTime{Time: day.Add(24*time.Hour - time.Second), Valid: true}
	}
	return tok, nil
}
