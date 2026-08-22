package storage

import (
	"context"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/config"
)

func iptr(v int64) *int64                 { return &v }
func sptr(v string) *string               { return &v }
func dptr(v time.Duration) *time.Duration { return &v }

// eqHost compares every hosts-table column by name so a positional mixup
// between INSERT/UPDATE args and SELECT columns fails loudly at the exact
// swapped pair instead of surfacing as a subtle value swap downstream.
func eqHost(t *testing.T, phase string, want, got Host) {
	t.Helper()
	if got.ID <= 0 {
		t.Errorf("%s: id not populated: %d", phase, got.ID)
	}
	if got.Name != want.Name {
		t.Errorf("%s: name: want %q got %q", phase, want.Name, got.Name)
	}
	if got.Connection != want.Connection {
		t.Errorf("%s: connection: want %q got %q", phase, want.Connection, got.Connection)
	}
	if got.Endpoint != want.Endpoint {
		t.Errorf("%s: endpoint: want %q got %q", phase, want.Endpoint, got.Endpoint)
	}
	if got.Port != want.Port {
		t.Errorf("%s: port: want %d got %d", phase, want.Port, got.Port)
	}
	if got.User != want.User {
		t.Errorf("%s: user: want %q got %q", phase, want.User, got.User)
	}
	if got.KeyPath != want.KeyPath {
		t.Errorf("%s: key_path: want %q got %q", phase, want.KeyPath, got.KeyPath)
	}
	if got.Sudo != want.Sudo {
		t.Errorf("%s: sudo: want %v got %v", phase, want.Sudo, got.Sudo)
	}
	if got.TimeoutRaw != want.TimeoutRaw {
		t.Errorf("%s: timeout: want %d got %d", phase, want.TimeoutRaw, got.TimeoutRaw)
	}
	if got.ProxyJump != want.ProxyJump {
		t.Errorf("%s: proxy_jump: want %q got %q", phase, want.ProxyJump, got.ProxyJump)
	}
	if len(got.Tags) != len(want.Tags) {
		t.Fatalf("%s: tags: want %v got %v", phase, want.Tags, got.Tags)
	}
	for i := range want.Tags {
		if got.Tags[i] != want.Tags[i] {
			t.Errorf("%s: tags[%d]: want %q got %q", phase, i, want.Tags[i], got.Tags[i])
		}
	}
	if got.CollectorPreference != want.CollectorPreference {
		t.Errorf("%s: collector_preference: want %q got %q", phase, want.CollectorPreference, got.CollectorPreference)
	}

	cmpInt64Ptr := func(col string, w, g *int64) {
		t.Helper()
		switch {
		case w == nil && g != nil:
			t.Errorf("%s: %s: want nil got %d", phase, col, *g)
		case w != nil && g == nil:
			t.Errorf("%s: %s: want %d got nil", phase, col, *w)
		case w != nil && g != nil && *w != *g:
			t.Errorf("%s: %s: want %d got %d", phase, col, *w, *g)
		}
	}
	cmpStrPtr := func(col string, w, g *string) {
		t.Helper()
		switch {
		case w == nil && g != nil:
			t.Errorf("%s: %s: want nil got %q", phase, col, *g)
		case w != nil && g == nil:
			t.Errorf("%s: %s: want %q got nil", phase, col, *w)
		case w != nil && g != nil && *w != *g:
			t.Errorf("%s: %s: want %q got %q", phase, col, *w, *g)
		}
	}
	cmpInt64Ptr("retry_max_retries", want.RetryMaxRetries, got.RetryMaxRetries)
	cmpInt64Ptr("retry_base_delay_ms", want.RetryBaseMs, got.RetryBaseMs)
	cmpInt64Ptr("retry_max_delay_ms", want.RetryMaxMs, got.RetryMaxMs)
	cmpInt64Ptr("ssh_timeout_ms", want.SshTimeoutMs, got.SshTimeoutMs)
	cmpInt64Ptr("collector_timeout_ms", want.CollectorTimeoutMs, got.CollectorTimeoutMs)
	cmpInt64Ptr("project_id", want.ProjectID, got.ProjectID)
	cmpStrPtr("ssh_host_key_policy", want.SSHHostKeyPolicy, got.SSHHostKeyPolicy)

	if got.ScriptName != want.ScriptName {
		t.Errorf("%s: script_name: want %q got %q", phase, want.ScriptName, got.ScriptName)
	}
	if got.ScriptCommand != want.ScriptCommand {
		t.Errorf("%s: script_command: want %q got %q", phase, want.ScriptCommand, got.ScriptCommand)
	}
	if got.ScriptParse != want.ScriptParse {
		t.Errorf("%s: script_parse: want %q got %q", phase, want.ScriptParse, got.ScriptParse)
	}
	cmpSNMP := func(col string, w, g string) {
		t.Helper()
		if w != g {
			t.Errorf("%s: %s: want %q got %q", phase, col, w, g)
		}
	}
	cmpSNMP("snmp_version", want.SNMPVersion, got.SNMPVersion)
	cmpSNMP("snmp_community", want.SNMPCommunity, got.SNMPCommunity)
	cmpSNMP("snmp_v3_user", want.SNMPv3User, got.SNMPv3User)
	cmpSNMP("snmp_v3_auth_proto", want.SNMPv3AuthProto, got.SNMPv3AuthProto)
	cmpSNMP("snmp_v3_auth_pass", want.SNMPv3AuthPass, got.SNMPv3AuthPass)
	cmpSNMP("snmp_v3_priv_proto", want.SNMPv3PrivProto, got.SNMPv3PrivProto)
	cmpSNMP("snmp_v3_priv_pass", want.SNMPv3PrivPass, got.SNMPv3PrivPass)
	cmpSNMP("snmp_extra_oids", want.SNMPExtraOIDs, got.SNMPExtraOIDs)
}

// fullHost returns a Host with every column set to a distinct value so any
// misordered bind or scan target shows up as a specific mismatch.
func fullHost(projectID int64) *Host {
	return &Host{
		Name:                "rt-full",
		Connection:          "ssh",
		Endpoint:            "192.0.2.10",
		Port:                2222,
		User:                "rt-user",
		KeyPath:             "/keys/rt",
		Sudo:                true,
		TimeoutRaw:          (31 * time.Second).Nanoseconds(),
		ProxyJump:           "jump-rt",
		Tags:                []string{"rt-a", "rt-b"},
		CollectorPreference: "snmp",
		RetryMaxRetries:     iptr(7),
		RetryBaseMs:         iptr(250),
		RetryMaxMs:          iptr(4000),
		SshTimeoutMs:        iptr(5500),
		CollectorTimeoutMs:  iptr(44000),
		ProjectID:           iptr(projectID),
		SSHHostKeyPolicy:    sptr("strict"),
		ScriptName:          "rt-script",
		ScriptCommand:       `/opt/rt/bin/stats --host {{.Host}} --port {{.Port}}`,
		ScriptParse:         "json",
		SNMPVersion:         "3",
		SNMPCommunity:       "rt-community",
		SNMPv3User:          "rt-user",
		SNMPv3AuthProto:     "SHA",
		SNMPv3AuthPass:      "rt-auth-pass",
		SNMPv3PrivProto:     "AES",
		SNMPv3PrivPass:      "rt-priv-pass",
		SNMPExtraOIDs:       "1.3.6.1.4.1.8072.999 temp_c",
	}
}

func TestHostFullFieldRoundtrip_CreateUpdateGet(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	proj, err := db.CreateProject(ctx, &Project{Name: "rt-proj", Type: "explicit"})
	if err != nil {
		t.Fatal(err)
	}

	want := fullHost(proj)
	id, err := db.CreateHost(ctx, want)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want.ID = id
	got, err := db.GetHost(ctx, id)
	if err != nil {
		t.Fatalf("get after create: %v", err)
	}
	eqHost(t, "after create", *want, *got)

	// Flip every writable column to a new distinct value; clear two nullable
	// ones to exercise NULL persistence through UPDATE.
	want.Connection = "snmp"
	want.Endpoint = "192.0.2.11"
	want.Port = 2223
	want.User = "rt-user2"
	want.KeyPath = "/keys/rt2"
	want.Sudo = false
	want.TimeoutRaw = (17 * time.Second).Nanoseconds()
	want.ProxyJump = "jump-rt2"
	want.Tags = []string{"rt-c"}
	want.CollectorPreference = "http"
	want.RetryMaxRetries = iptr(9)
	want.RetryBaseMs = iptr(500)
	want.RetryMaxMs = iptr(8000)
	want.SshTimeoutMs = nil
	want.CollectorTimeoutMs = iptr(66000)
	want.SSHHostKeyPolicy = nil
	want.ScriptName = "rt-script-2"
	want.ScriptCommand = `cat /var/lib/metrics.csv`
	want.ScriptParse = "csv"
	want.SNMPVersion = "2c"
	want.SNMPCommunity = "rt-community-2"
	want.SNMPv3User = ""
	want.SNMPv3AuthProto = ""
	want.SNMPv3AuthPass = ""
	want.SNMPv3PrivProto = ""
	want.SNMPv3PrivPass = ""
	want.SNMPExtraOIDs = ""

	if err := db.UpdateHost(ctx, want); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = db.GetHost(ctx, id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	eqHost(t, "after update", *want, *got)

	// Also exercise the name-keyed read path.
	byName, err := db.GetHostByName(ctx, want.Name)
	if err != nil || byName == nil {
		t.Fatalf("get by name: %v %v", byName, err)
	}
	eqHost(t, "after update (by name)", *want, *byName)
}

func TestSeedHosts_FullFieldRoundtrip(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	proj, err := db.CreateProject(ctx, &Project{Name: "seed-proj", Type: "explicit"})
	if err != nil {
		t.Fatal(err)
	}

	timeout := 11 * time.Second
	baseDelay := 300 * time.Millisecond
	maxDelay := 5 * time.Second
	sshTimeout := 6 * time.Second
	collTimeout := 33 * time.Second
	seed := []config.Host{{
		Name:                "rt-seed",
		Connection:          "ssh",
		Endpoint:            "198.51.100.20",
		Port:                2201,
		User:                "seed-user",
		KeyPath:             "/keys/seed",
		Sudo:                true,
		Timeout:             timeout,
		ProxyJump:           "jump-seed",
		Tags:                []string{"seed-a"},
		CollectorPreference: "prom",
		ProjectID:           &proj,
		RetryMaxRetries:     iptr(9),
		RetryBaseDelay:      &baseDelay,
		RetryMaxDelay:       &maxDelay,
		SSHTimeout:          &sshTimeout,
		CollectorTimeout:    &collTimeout,
		SSHHostKeyPolicy:    sptr("auto"),
	}}
	if err := db.SeedHosts(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := db.GetHostByName(ctx, "rt-seed")
	if err != nil || got == nil {
		t.Fatalf("get seeded host: %v %v", got, err)
	}
	want := Host{
		Name:                seed[0].Name,
		Connection:          seed[0].Connection,
		Endpoint:            seed[0].Endpoint,
		Port:                seed[0].Port,
		User:                seed[0].User,
		KeyPath:             seed[0].KeyPath,
		Sudo:                seed[0].Sudo,
		TimeoutRaw:          timeout.Nanoseconds(),
		ProxyJump:           seed[0].ProxyJump,
		Tags:                seed[0].Tags,
		CollectorPreference: seed[0].CollectorPreference,
		RetryMaxRetries:     seed[0].RetryMaxRetries,
		RetryBaseMs:         iptr(baseDelay.Milliseconds()),
		RetryMaxMs:          iptr(maxDelay.Milliseconds()),
		SshTimeoutMs:        iptr(sshTimeout.Milliseconds()),
		CollectorTimeoutMs:  iptr(collTimeout.Milliseconds()),
		ProjectID:           seed[0].ProjectID,
		SSHHostKeyPolicy:    seed[0].SSHHostKeyPolicy,
	}
	eqHost(t, "seeded from config", want, *got)

	// Re-seed with every yaml-owned column changed: the ON CONFLICT branch
	// must refresh exactly those and leave operational columns untouched.
	seed[0].Connection = "snmp"
	seed[0].Endpoint = "198.51.100.21"
	seed[0].Port = 2202
	seed[0].User = "seed-user2"
	seed[0].KeyPath = "/keys/seed2"
	seed[0].Sudo = false
	seed[0].ProxyJump = "jump-seed2"
	seed[0].Tags = []string{"seed-b"}
	if err := db.SeedHosts(seed); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	reseeded, err := db.GetHostByName(ctx, "rt-seed")
	if err != nil || reseeded == nil {
		t.Fatalf("get re-seeded host: %v %v", reseeded, err)
	}
	wantReseeded := Host{
		Name:                "rt-seed",
		Connection:          "snmp",
		Endpoint:            "198.51.100.21",
		Port:                2202,
		User:                "seed-user2",
		KeyPath:             "/keys/seed2",
		Sudo:                false,
		TimeoutRaw:          timeout.Nanoseconds(),
		ProxyJump:           "jump-seed2",
		Tags:                []string{"seed-b"},
		CollectorPreference: "prom",
		RetryMaxRetries:     iptr(9),
		RetryBaseMs:         iptr(baseDelay.Milliseconds()),
		RetryMaxMs:          iptr(maxDelay.Milliseconds()),
		SshTimeoutMs:        iptr(sshTimeout.Milliseconds()),
		CollectorTimeoutMs:  iptr(collTimeout.Milliseconds()),
		ProjectID:           &proj,
		SSHHostKeyPolicy:    sptr("auto"),
	}
	eqHost(t, "after re-seed", wantReseeded, *reseeded)
}
