package dashboard

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

// legacyXORHash mirrors the pre-SHA-256 scheme for seeding legacy rows.
func legacyXORHash(token string) string {
	b := []byte(token)
	h := make([]byte, 32)
	for i, c := range b {
		h[i%32] ^= c
	}
	return hex.EncodeToString(h)
}

func createTestToken(t *testing.T, s *Server, name, scopes string) (plain string, id int64) {
	t.Helper()
	plain, id, err := s.db.CreateAPIToken(context.Background(), &storage.APIToken{Name: name, Scopes: scopes})
	if err != nil {
		t.Fatalf("create token %s: %v", name, err)
	}
	return plain, id
}

func tokenRequest(s *Server, target, method, plainToken string) *httptest.ResponseRecorder {
	handler := s.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	req := httptest.NewRequest(method, target, nil)
	if plainToken != "" {
		req.Header.Set("Authorization", "Bearer "+plainToken)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	s := newTestServer(t)

	t.Run("missing header redirects to login", func(t *testing.T) {
		rec := tokenRequest(s, "/api/alerts", http.MethodGet, "")
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected 303 redirect, got %d", rec.Code)
		}
	})

	t.Run("invalid token rejected 401", func(t *testing.T) {
		rec := tokenRequest(s, "/api/alerts", http.MethodGet, "not-a-real-token")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("valid read token passes GET", func(t *testing.T) {
		plain, _ := createTestToken(t, s, "reader", "read")
		rec := tokenRequest(s, "/api/alerts", http.MethodGet, plain)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("read token blocked from mutations", func(t *testing.T) {
		plain, _ := createTestToken(t, s, "reader2", "read")
		rec := tokenRequest(s, "/api/hosts", http.MethodPost, plain)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rec.Code)
		}
	})

	t.Run("write token allowed mutation", func(t *testing.T) {
		plain, _ := createTestToken(t, s, "writer", "write")
		rec := tokenRequest(s, "/api/projects", http.MethodPost, plain)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("write token blocked from config endpoints", func(t *testing.T) {
		plain, _ := createTestToken(t, s, "writer2", "write")
		rec := tokenRequest(s, "/api/alert-rules", http.MethodGet, plain)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for config endpoint with write token, got %d", rec.Code)
		}
	})

	t.Run("admin token reaches config endpoints", func(t *testing.T) {
		plain, _ := createTestToken(t, s, "adminer", "admin")
		rec := tokenRequest(s, "/api/api-tokens", http.MethodGet, plain)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("expired token rejected", func(t *testing.T) {
		plain, id := createTestToken(t, s, "old", "read")
		tok, err := s.db.GetAPIToken(context.Background(), id)
		if err != nil {
			t.Fatalf("get token: %v", err)
		}
		tok.ExpiresAt = sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true}
		if err := s.db.UpdateAPIToken(context.Background(), tok); err != nil {
			t.Fatalf("expire token: %v", err)
		}
		rec := tokenRequest(s, "/api/alerts", http.MethodGet, plain)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for expired token, got %d", rec.Code)
		}
	})
}

func TestAuthMiddleware_LegacyHashRejected(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	plain := "legacyplaintexttoken1234567890abcdef"
	_, id, err := s.db.CreateAPIToken(ctx, &storage.APIToken{Name: "legacy", Scopes: "read"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	// Overwrite with the old XOR hash as a pre-upgrade deployment would have.
	if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET token_hash = ? WHERE id = ?`, legacyXORHash(plain), id); err != nil {
		t.Fatalf("seed legacy hash: %v", err)
	}

	rec := tokenRequest(s, "/api/alerts", http.MethodGet, plain)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("legacy-hashed tokens must be rejected after SHA-256 upgrade, got %d", rec.Code)
	}
}

func TestAPITokenRequest_Validation(t *testing.T) {
	valid := func(mutate func(*apiTokenRequest)) apiTokenRequest {
		req := apiTokenRequest{Name: "n", Scopes: "read"}
		mutate(&req)
		return req
	}

	t.Run("defaults empty scopes to read", func(t *testing.T) {
		req := valid(func(r *apiTokenRequest) { r.Scopes = "" })
		tok, err := req.toAPIToken()
		if err != nil || tok.Scopes != "read" {
			t.Fatalf("got %v, %v", tok, err)
		}
	})

	t.Run("normalizes case", func(t *testing.T) {
		req := valid(func(r *apiTokenRequest) { r.Scopes = "ADMIN" })
		tok, err := req.toAPIToken()
		if err != nil || tok.Scopes != "admin" {
			t.Fatalf("got %v, %v", tok, err)
		}
	})

	t.Run("rejects unknown scope", func(t *testing.T) {
		req := valid(func(r *apiTokenRequest) { r.Scopes = "read,write" })
		if _, err := req.toAPIToken(); err == nil {
			t.Fatal("expected error for multi-value scope")
		}
	})

	t.Run("rejects empty name", func(t *testing.T) {
		req := valid(func(r *apiTokenRequest) { r.Name = "  " })
		if _, err := req.toAPIToken(); err == nil {
			t.Fatal("expected error for blank name")
		}
	})

	t.Run("parses expiry to end of day", func(t *testing.T) {
		date := "2026-09-01"
		req := valid(func(r *apiTokenRequest) { r.ExpiresAt = &date })
		tok, err := req.toAPIToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 9, 1, 23, 59, 59, 0, time.UTC)
		if !tok.ExpiresAt.Valid || !tok.ExpiresAt.Time.UTC().Equal(want) {
			t.Fatalf("expected %v, got %+v", want, tok.ExpiresAt)
		}
	})

	t.Run("rejects malformed expiry", func(t *testing.T) {
		bad := "09/01/2026"
		req := valid(func(r *apiTokenRequest) { r.ExpiresAt = &bad })
		if _, err := req.toAPIToken(); err == nil {
			t.Fatal("expected error for bad date format")
		}
	})
}

func TestAuthMiddleware_ScopedTokenCannotWidenViaParam(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	projectA, err := s.db.CreateProject(ctx, &storage.Project{Name: "locked", Type: "explicit"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	projectB, err := s.db.CreateProject(ctx, &storage.Project{Name: "other", Type: "explicit"})
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}
	hosts, _ := s.db.GetHosts()
	if len(hosts) < 2 {
		t.Fatal("need 2 hosts")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE hosts SET project_id = ? WHERE id = ?`, projectB, hosts[1].ID); err != nil {
		t.Fatalf("assign host: %v", err)
	}
	if err := s.db.InsertAlert(ctx, storage.Alert{HostID: hosts[1].ID, Type: "threshold", Metric: "mem.used_pct", Severity: "critical", Message: "m", Value: 95, Threshold: 90, FiredAt: time.Now()}); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	plain := createScopedToken(t, s, projectA)

	handler := s.authMiddleware(s.projectMiddleware(s.handleAPIAlerts))
	req := httptest.NewRequest(http.MethodGet, "/api/alerts?project_id="+strconv.FormatInt(projectB, 10), nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var alerts []storage.AlertWithHost
	if err := json.Unmarshal(rec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	for _, a := range alerts {
		if a.Metric == "mem.used_pct" {
			t.Fatal("scoped token escaped its project via query param")
		}
	}
}

// createScopedToken creates a read token bound to the given project.
func createScopedToken(t *testing.T, s *Server, projectID int64) string {
	t.Helper()
	plain, _, err := s.db.CreateAPIToken(context.Background(), &storage.APIToken{Name: "scoped-token", Scopes: "read", ProjectID: &projectID})
	if err != nil {
		t.Fatalf("create scoped token: %v", err)
	}
	return plain
}

func TestAuthMiddleware_TokenProjectScoping(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	projectA, err := s.db.CreateProject(ctx, &storage.Project{Name: "tok-proj", Type: "explicit"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	hosts, _ := s.db.GetHosts()
	if len(hosts) == 0 {
		t.Fatal("no hosts seeded")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE hosts SET project_id = ? WHERE id = ?`, projectA, hosts[0].ID); err != nil {
		t.Fatalf("assign host: %v", err)
	}
	if err := s.db.InsertAlert(ctx, storage.Alert{HostID: hosts[0].ID, Type: "threshold", Metric: "cpu.user_pct", Severity: "warning", Message: "m", Value: 90, Threshold: 80, FiredAt: time.Now()}); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	plain, _ := createTestToken(t, s, "scoped", "read")
	// Give the token a project scope.
	tok, _ := s.db.GetAPITokens(ctx)
	for i := range tok {
		if tok[i].Name == "scoped" {
			tok[i].ProjectID = &projectA
			if err := s.db.UpdateAPIToken(ctx, &tok[i]); err != nil {
				t.Fatalf("scope token: %v", err)
			}
		}
	}

	handler := s.authMiddleware(s.projectMiddleware(s.handleAPIAlerts))
	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var scoped []storage.AlertWithHost
	if err := json.Unmarshal(rec.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Metric != "cpu.user_pct" {
		t.Fatalf("expected only project-scoped alert, got %+v", scoped)
	}
}

func TestTokenRateLimiter(t *testing.T) {
	l := newTokenRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow(7) {
			t.Fatalf("request %d should pass", i+1)
		}
	}
	if l.Allow(7) {
		t.Fatal("request 4 should be limited")
	}
	if !l.Allow(8) {
		t.Fatal("other tokens unaffected")
	}
}

func TestLastUsedRecorder_Throttles(t *testing.T) {
	r := newLastUsedRecorder(time.Minute)
	now := time.Now()
	if !r.ShouldRecord(1, now) {
		t.Fatal("first call records")
	}
	if r.ShouldRecord(1, now.Add(30*time.Second)) {
		t.Fatal("within interval throttled")
	}
	if !r.ShouldRecord(1, now.Add(61*time.Second)) {
		t.Fatal("after interval records again")
	}
}

func TestAuthMiddleware_RateLimited(t *testing.T) {
	s := newTestServer(t)
	s.tokenLimiter = newTokenRateLimiter(2, time.Minute)
	plain, _ := createTestToken(t, s, "hammer", "read")

	for i := 0; i < 2; i++ {
		if rec := tokenRequest(s, "/api/alerts", http.MethodGet, plain); rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}
	rec := tokenRequest(s, "/api/alerts", http.MethodGet, plain)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
}

func TestRequiredScope(t *testing.T) {
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/hosts", scopeRead},
		{http.MethodGet, "/api/alerts", scopeRead},
		{http.MethodDelete, "/api/hosts/5", scopeWrite},
		{http.MethodPost, "/api/projects", scopeWrite},
		{http.MethodGet, "/api/alert-rules", scopeAdmin},
		{http.MethodPost, "/api/api-tokens", scopeAdmin},
		{http.MethodPut, "/api/notification-channels/3", scopeAdmin},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if got := requiredScope(req); got != tc.want {
			t.Errorf("%s %s: got scope %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestAPITokenFromContext(t *testing.T) {
	if got := APITokenFromContext(context.Background()); got != nil {
		t.Errorf("expected nil outside request flow, got %+v", got)
	}
	want := &APITokenInfo{ID: 5, Name: "n", Scopes: "read"}
	ctx := context.WithValue(context.Background(), apiTokenCtxKey, want)
	got := APITokenFromContext(ctx)
	if got != want {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}
