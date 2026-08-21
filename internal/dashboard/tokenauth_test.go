package dashboard

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestAuthMiddleware_LegacyHashMigrated(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	plain := "legacyplaintexttoken1234567890abcdef"
	_, id, err := s.db.CreateAPIToken(ctx, &storage.APIToken{Name: "legacy", Scopes: "read"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	// Overwrite with legacy XOR hash as an old deployment would have stored.
	if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET token_hash = ? WHERE id = ?`, legacyXORHash(plain), id); err != nil {
		t.Fatalf("seed legacy hash: %v", err)
	}

	rec := tokenRequest(s, "/api/alerts", http.MethodGet, plain)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy token should authenticate, got %d", rec.Code)
	}

	row := s.db.QueryRowContext(ctx, `SELECT token_hash FROM api_tokens WHERE id = ?`, id)
	var stored string
	if err := row.Scan(&stored); err != nil {
		t.Fatalf("scan hash: %v", err)
	}
	if stored != storage.HashAPIToken(plain) {
		t.Error("expected token re-hashed to SHA-256 after successful use")
	}
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
	ctx := context.WithValue(context.Background(), apiTokenContextKey, want)
	got := APITokenFromContext(ctx)
	if got != want {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}
