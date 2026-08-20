package dashboard

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestToRateSeries(t *testing.T) {
	data := [][2]float64{
		{1000, 10},
		{1060, 20},
		{1120, 30},
	}
	got := toRateSeries(data)
	if len(got) != 2 {
		t.Fatalf("got %d points, want 2", len(got))
	}
	if got[0][0] != 1060 || got[0][1] != 10.0/60.0 {
		t.Errorf("got %v, want rate 10/60 at t=1060", got[0])
	}
	if got[1][1] != 10.0/60.0 {
		t.Errorf("got %v, want 10/60 at t=1120", got[1])
	}
}

func TestToRateSeries_Empty(t *testing.T) {
	if got := toRateSeries(nil); len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func newTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func TestAllTemplatesParse(t *testing.T) {
	s := &Server{logger: newTestLogger(t)}
	s.loadTemplates()

	want := []string{"index.html", "host.html", "compare.html", "projects.html", "alerts.html", "monitor.html", "login.html"}
	for _, name := range want {
		if _, ok := s.templates[name]; !ok {
			t.Errorf("template %s not loaded", name)
			continue
		}
		entry := "base"
		if name == "login.html" {
			entry = "login.html"
		}
		if tpl := s.templates[name].Lookup(entry); tpl == nil {
			t.Errorf("template %s has no %q entry", name, entry)
		}
	}
}

func TestBaseTemplateExecutesContent(t *testing.T) {
	s := &Server{logger: newTestLogger(t)}
	s.loadTemplates()
	// compare.html is rendered with nil data by its handler, so it exercises
	// the base shell + content block end to end without needing storage data.
	tmpl, ok := s.templates["compare.html"]
	if !ok {
		t.Fatal("compare.html not loaded")
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", nil); err != nil {
		t.Fatalf("base template failed to execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"<nav", "theme-toggle", "Skip to content", "Multi-Host Comparison"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("rendered base+compare missing %q", want)
		}
	}
}