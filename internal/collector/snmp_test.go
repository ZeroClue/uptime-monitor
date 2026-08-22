package collector

import (
	"context"
	"fmt"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// fakeSNMPSession replays canned Get/BulkWalk results.
type fakeSNMPSession struct {
	get    func(oids []string) ([]gosnmp.SnmpPDU, error)
	walk   func(root string) ([]gosnmp.SnmpPDU, error)
	closed bool
}

func (f *fakeSNMPSession) Get(oids []string) ([]gosnmp.SnmpPDU, error) {
	return f.get(oids)
}

func (f *fakeSNMPSession) BulkWalkAll(root string) ([]gosnmp.SnmpPDU, error) {
	return f.walk(root)
}

func (f *fakeSNMPSession) Close() error { f.closed = true; return nil }

func pdu(name string, typ gosnmp.Asn1BER, value any) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: name, Type: typ, Value: value}
}

func snmpTestHost() Host {
	return Host{
		ID: 5, Name: "sw-core", Connection: "snmp", Endpoint: "10.0.0.9", Port: 161,
		CollectorPreference: "snmp",
		SNMPVersion:         "2c", SNMPCommunity: "public",
		SNMPExtraOIDs: "1.3.6.1.4.1.999.1.0 temp_c\n1.3.6.1.4.1.999.2.0 fans_rpm",
	}
}

func TestSNMPCollector_Gating(t *testing.T) {
	c := NewSNMPCollector()
	ctx := context.Background()

	host := snmpTestHost()
	host.Connection = "ssh"
	if _, err := c.Collect(ctx, host); err == nil {
		t.Error("expected error for non-snmp connection")
	}

	host = snmpTestHost()
	host.SNMPVersion = "1"
	if _, err := c.Collect(ctx, host); err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestSNMPCollector_InterfaceStats(t *testing.T) {
	session := &fakeSNMPSession{
		get: func(oids []string) ([]gosnmp.SnmpPDU, error) { return nil, nil },
		walk: func(root string) ([]gosnmp.SnmpPDU, error) {
			switch root {
			case oidIfDescr:
				return []gosnmp.SnmpPDU{
					pdu(oidIfDescr+".1", gosnmp.OctetString, []byte("eth0")),
					pdu(oidIfDescr+".2", gosnmp.OctetString, []byte("vlan 100")),
				}, nil
			case oidIfOperStatus:
				return []gosnmp.SnmpPDU{
					pdu(oidIfOperStatus+".1", gosnmp.Integer, int(1)),
					pdu(oidIfOperStatus+".2", gosnmp.Integer, int(2)),
				}, nil
			case oidIfInOctets:
				return []gosnmp.SnmpPDU{
					pdu(oidIfInOctets+".1", gosnmp.Counter32, uint32(1000)),
					pdu(oidIfInOctets+".2", gosnmp.Counter32, uint32(2000)),
				}, nil
			case oidIfOutOctets:
				return []gosnmp.SnmpPDU{
					pdu(oidIfOutOctets+".1", gosnmp.Counter32, uint32(3000)),
					pdu(oidIfOutOctets+".2", gosnmp.Counter32, uint32(4000)),
				}, nil
			case oidIfInErrors:
				return []gosnmp.SnmpPDU{pdu(oidIfInErrors+".1", gosnmp.Counter32, uint32(9))}, nil
			default:
				return nil, nil
			}
		},
	}

	samples := collectWith(t, session, nil)
	want := map[string]float64{
		"snmp.iface.eth0.in_octets":      1000,
		"snmp.iface.eth0.out_octets":     3000,
		"snmp.iface.eth0.in_errors":      9,
		"snmp.iface.vlan_100.in_octets":  2000,
		"snmp.iface.vlan_100.out_octets": 4000,
		"snmp.iface.eth0.up":             1,
		"snmp.iface.vlan_100.up":         0, // operStatus 2 != 1
	}
	got := sampleMap(samples)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: want %v got %v", k, v, got[k])
		}
	}
	for _, s := range samples {
		if s.HostID != 5 || s.Collector != "snmp" {
			t.Errorf("sample binding wrong: %+v", s)
			break
		}
	}
}

func TestSNMPCollector_CPUAndMemory(t *testing.T) {
	session := &fakeSNMPSession{
		get: func(oids []string) ([]gosnmp.SnmpPDU, error) {
			var out []gosnmp.SnmpPDU
			for _, o := range oids {
				switch o {
				case oidUcdMemAvailReal:
					out = append(out, pdu(o, gosnmp.Integer, int(2000)))
				case oidUcdMemTotalReal:
					out = append(out, pdu(o, gosnmp.Integer, int(8000)))
				default:
					out = append(out, pdu(o, gosnmp.NoSuchObject, nil))
				}
			}
			return out, nil
		},
		walk: func(root string) ([]gosnmp.SnmpPDU, error) {
			if root == oidHrProcessorLoadPrefix {
				return []gosnmp.SnmpPDU{
					pdu(oidHrProcessorLoadPrefix+".0", gosnmp.Integer, int(30)),
					pdu(oidHrProcessorLoadPrefix+".1", gosnmp.Integer, int(50)),
				}, nil
			}
			return nil, nil
		},
	}

	got := sampleMap(collectWith(t, session, nil))
	if got["snmp.cpu.user_pct"] != 40 {
		t.Errorf("cpu: %v", got["snmp.cpu.user_pct"])
	}
	if got["snmp.mem.total_bytes"] != 8192000 || got["snmp.mem.used_bytes"] != 6144000 {
		t.Errorf("mem: total=%v used=%v", got["snmp.mem.total_bytes"], got["snmp.mem.used_bytes"])
	}
}

func TestSNMPCollector_UCDLoad(t *testing.T) {
	session := &fakeSNMPSession{
		get: func(oids []string) ([]gosnmp.SnmpPDU, error) {
			var out []gosnmp.SnmpPDU
			loads := map[string]float64{
				oidUcdLoad1: 0.5, oidUcdLoad5: 1.25, oidUcdLoad15: 2.0,
			}
			for _, o := range oids {
				if v, ok := loads[o]; ok {
					out = append(out, pdu(o, gosnmp.OctetString, fmt.Sprintf("%.2f", v)))
				} else {
					out = append(out, pdu(o, gosnmp.NoSuchObject, nil))
				}
			}
			return out, nil
		},
		walk: func(string) ([]gosnmp.SnmpPDU, error) { return nil, nil },
	}

	got := sampleMap(collectWith(t, session, nil))
	for k, want := range map[string]float64{"snmp.load.1m": 0.5, "snmp.load.5m": 1.25, "snmp.load.15m": 2.0} {
		if got[k] != want {
			t.Errorf("%s: want %v got %v", k, want, got[k])
		}
	}
}

func TestSNMPCollector_HRStorageDisksAndRAM(t *testing.T) {
	session := &fakeSNMPSession{
		get: func([]string) ([]gosnmp.SnmpPDU, error) { return nil, nil },
		walk: func(root string) ([]gosnmp.SnmpPDU, error) {
			ram := "1.3.6.1.2.1.25.2.1.2"  // hrStorageRam
			disk := "1.3.6.1.2.1.25.2.1.4" // hrStorageFixedDisk
			switch root {
			case oidHrStorageType:
				return []gosnmp.SnmpPDU{
					pdu(oidHrStorageType+".1", gosnmp.ObjectIdentifier, ram),
					pdu(oidHrStorageType+".2", gosnmp.ObjectIdentifier, disk),
				}, nil
			case oidHrStorageDescr:
				return []gosnmp.SnmpPDU{
					pdu(oidHrStorageDescr+".1", gosnmp.OctetString, []byte("Physical memory")),
					pdu(oidHrStorageDescr+".2", gosnmp.OctetString, []byte("/")),
				}, nil
			case oidHrStorageUnit:
				return []gosnmp.SnmpPDU{
					pdu(oidHrStorageUnit+".1", gosnmp.Integer, int(1024)),
					pdu(oidHrStorageUnit+".2", gosnmp.Integer, int(512)),
				}, nil
			case oidHrStorageSize:
				return []gosnmp.SnmpPDU{
					pdu(oidHrStorageSize+".1", gosnmp.Integer, int(100)),
					pdu(oidHrStorageSize+".2", gosnmp.Integer, int(1000)),
				}, nil
			case oidHrStorageUsed:
				return []gosnmp.SnmpPDU{
					pdu(oidHrStorageUsed+".1", gosnmp.Integer, int(40)),
					pdu(oidHrStorageUsed+".2", gosnmp.Integer, int(250)),
				}, nil
			default:
				return nil, nil
			}
		},
	}

	got := sampleMap(collectWith(t, session, nil))
	checks := map[string]float64{
		"snmp.mem.total_bytes":    102400,
		"snmp.mem.used_bytes":     40960,
		"snmp.disk._.total_bytes": 512000,
		"snmp.disk._.used_bytes":  128000,
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s: want %v got %v", k, want, got[k])
		}
	}
}

func TestSNMPCollector_ExtraOIDs(t *testing.T) {
	session := &fakeSNMPSession{
		get: func(oids []string) ([]gosnmp.SnmpPDU, error) {
			var out []gosnmp.SnmpPDU
			for _, o := range oids {
				switch o {
				case "1.3.6.1.4.1.999.1.0":
					out = append(out, pdu(o, gosnmp.Integer, int(72)))
				default:
					out = append(out, pdu(o, gosnmp.NoSuchObject, nil))
				}
			}
			return out, nil
		},
		walk: func(string) ([]gosnmp.SnmpPDU, error) { return nil, nil },
	}

	got := sampleMap(collectWith(t, session, nil))
	if got["snmp.custom.temp_c"] != 72 {
		t.Errorf("custom oid: %v", got["snmp.custom.temp_c"])
	}
	if _, ok := got["snmp.custom.fans_rpm"]; ok {
		t.Error("missing OID must not produce a sample")
	}
}

func TestParseExtraOIDs(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]string
	}{
		{"", map[string]string{}},
		{"1.2.3 temp\n4.5.6 fan_rpm", map[string]string{"1.2.3": "temp", "4.5.6": "fan_rpm"}},
		{"\n  1.2.3   spaced_name \n# comment\nbad-line", map[string]string{"1.2.3": "spaced_name"}},
	}
	for _, c := range cases {
		got := parseExtraOIDs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parse %q: want %d got %d (%v)", c.in, len(c.want), len(got), got)
			continue
		}
		for oid, name := range c.want {
			if got[oid] != name {
				t.Errorf("parse %q: %s -> %q, want %q", c.in, oid, got[oid], name)
			}
		}
	}
}

func TestSNMPCollector_SessionClosed(t *testing.T) {
	session := &fakeSNMPSession{
		get: func(oids []string) ([]gosnmp.SnmpPDU, error) {
			var out []gosnmp.SnmpPDU
			for _, o := range oids {
				if o == oidUcdLoad1 {
					out = append(out, pdu(o, gosnmp.OctetString, "0.10"))
				} else {
					out = append(out, pdu(o, gosnmp.NoSuchObject, nil))
				}
			}
			return out, nil
		},
		walk: func(string) ([]gosnmp.SnmpPDU, error) { return nil, nil },
	}
	c := NewSNMPCollector(WithSNMPDialer(func(Host) (snmpSession, error) { return session, nil }))
	if _, err := c.Collect(context.Background(), snmpTestHost()); err != nil {
		t.Fatal(err)
	}
	if !session.closed {
		t.Error("session not closed after collect")
	}
}

func collectWith(t *testing.T, session *fakeSNMPSession, mutate func(*Host)) []Sample {
	t.Helper()
	c := NewSNMPCollector(WithSNMPDialer(func(Host) (snmpSession, error) { return session, nil }))
	host := snmpTestHost()
	if mutate != nil {
		mutate(&host)
	}
	samples, err := c.Collect(context.Background(), host)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return samples
}

func sampleMap(samples []Sample) map[string]float64 {
	m := make(map[string]float64, len(samples))
	for _, s := range samples {
		m[s.Metric] = s.Value
	}
	return m
}

func TestDialSNMPConfigBuilding(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Host)
		check   func(*testing.T, *gosnmp.GoSNMP)
		wantErr bool
	}{
		{
			name:   "v2c community",
			mutate: func(h *Host) { h.SNMPVersion = "2c"; h.SNMPCommunity = "pub" },
			check: func(t *testing.T, g *gosnmp.GoSNMP) {
				if g.Version != gosnmp.Version2c || g.Community != "pub" {
					t.Errorf("v2c: %+v", g)
				}
			},
		},
		{
			name: "v3 authpriv sha/aes",
			mutate: func(h *Host) {
				h.SNMPVersion = "3"
				h.SNMPv3User = "monitor"
				h.SNMPv3AuthProto = "SHA256"
				h.SNMPv3AuthPass = "ap"
				h.SNMPv3PrivProto = "AES256"
				h.SNMPv3PrivPass = "pp"
			},
			check: func(t *testing.T, g *gosnmp.GoSNMP) {
				if g.MsgFlags&^gosnmp.Reportable != gosnmp.AuthPriv {
					t.Errorf("flags: %v", g.MsgFlags)
				}
				sp := g.SecurityParameters.(*gosnmp.UsmSecurityParameters)
				if sp.AuthenticationProtocol != gosnmp.SHA256 || sp.PrivacyProtocol != gosnmp.AES256C {
					t.Errorf("protocols: %+v", sp)
				}
			},
		},
		{
			name:   "v3 noauthnopriv when no passwords",
			mutate: func(h *Host) { h.SNMPVersion = "3"; h.SNMPv3User = "u" },
			check: func(t *testing.T, g *gosnmp.GoSNMP) {
				if g.MsgFlags&^gosnmp.Reportable != gosnmp.NoAuthNoPriv {
					t.Errorf("flags: %v", g.MsgFlags)
				}
			},
		},
		{
			name:    "bad auth proto",
			mutate:  func(h *Host) { h.SNMPVersion = "3"; h.SNMPv3AuthPass = "x"; h.SNMPv3AuthProto = "CRC" },
			wantErr: true,
		},
		{
			name:    "unsupported version",
			mutate:  func(h *Host) { h.SNMPVersion = "1" },
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := snmpTestHost()
			host.Endpoint = "127.0.0.1" // never contacted: Connect fails only on network error for udp dial? UDP connect is lazy.
			tc.mutate(&host)
			sess, err := dialSNMP(host)
			if tc.wantErr {
				if err == nil {
					sess.Close()
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer sess.Close()
			g := sess.(goSNMPSession).g
			if g.Port != 161 {
				t.Errorf("port: %d", g.Port)
			}
			tc.check(t, g)
		})
	}
}

func TestSNMPOIDs_StandardValuesPinned(t *testing.T) {
	// Literal pins guard against transcription drift in the const block;
	// .4.11.x was silently wrong here once already (it's memTotalFree).
	if oidUcdMemTotalReal != "1.3.6.1.4.1.2021.4.5.0" {
		t.Errorf("memTotalReal: %s", oidUcdMemTotalReal)
	}
	if oidUcdMemAvailReal != "1.3.6.1.4.1.2021.4.6.0" {
		t.Errorf("memAvailReal: %s", oidUcdMemAvailReal)
	}
}

func TestSNMPCollector_DuplicateDescriptionsDisambiguated(t *testing.T) {
	session := &fakeSNMPSession{
		get: func([]string) ([]gosnmp.SnmpPDU, error) { return nil, nil },
		walk: func(root string) ([]gosnmp.SnmpPDU, error) {
			switch root {
			case oidIfDescr:
				// Descriptive text that would collide if used verbatim.
				return []gosnmp.SnmpPDU{
					pdu(oidIfDescr+".1", gosnmp.OctetString, []byte("port")),
					pdu(oidIfDescr+".2", gosnmp.OctetString, []byte("port")),
					pdu(oidIfDescr+".10", gosnmp.OctetString, []byte("port")),
				}, nil
			case oidIfInOctets:
				return []gosnmp.SnmpPDU{
					pdu(oidIfInOctets+".1", gosnmp.Counter32, uint32(11)),
					pdu(oidIfInOctets+".2", gosnmp.Counter32, uint32(22)),
					pdu(oidIfInOctets+".10", gosnmp.Counter32, uint32(33)),
				}, nil
			default:
				return nil, nil
			}
		},
	}

	got := sampleMap(collectWith(t, session, nil))
	// Duplicate descriptions get index-suffixed deterministically.
	for metric, want := range map[string]float64{
		"snmp.iface.port.in_octets":    11,
		"snmp.iface.port_2.in_octets":  22,
		"snmp.iface.port_10.in_octets": 33,
	} {
		if got[metric] != want {
			t.Errorf("%s: want %v got %v", metric, want, got[metric])
		}
	}
}

func TestSNMPCollector_IfNamePreferredOverDescr(t *testing.T) {
	session := &fakeSNMPSession{
		get: func([]string) ([]gosnmp.SnmpPDU, error) { return nil, nil },
		walk: func(root string) ([]gosnmp.SnmpPDU, error) {
			switch root {
			case oidIfName:
				return []gosnmp.SnmpPDU{
					pdu(oidIfName+".1", gosnmp.OctetString, []byte("Gi0/1")),
				}, nil
			case oidIfDescr:
				return []gosnmp.SnmpPDU{
					pdu(oidIfDescr+".1", gosnmp.OctetString, []byte("GigabitEthernet0/1")),
				}, nil
			case oidIfInOctets:
				return []gosnmp.SnmpPDU{
					pdu(oidIfInOctets+".1", gosnmp.Counter32, uint32(11)),
				}, nil
			default:
				return nil, nil
			}
		},
	}
	got := sampleMap(collectWith(t, session, nil))
	if _, ok := got["snmp.iface.gi0_1.in_octets"]; !ok {
		t.Errorf("ifName should win over ifDescr: %v", got)
	}
	if _, ok := got["snmp.iface.gigabitethernet0/1.in_octets"]; ok {
		t.Error("ifDescr leaked despite ifName being present")
	}
}
