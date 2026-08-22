package collector

import (
	"fmt"
	"time"

	"github.com/gosnmp/gosnmp"
)

const (
	defaultSNMPTimeout = 5 * time.Second
	snmpRetries        = 1
)

// dialSNMP opens an SNMP session for the host's connection settings. The
// per-host Timeout (DB-owned operations data) bounds each exchange.
func dialSNMP(host Host) (snmpSession, error) {
	port := host.Port
	if port == 0 {
		port = 161
	}
	timeout := defaultSNMPTimeout
	if host.Timeout > 0 {
		timeout = host.Timeout
	}
	g := &gosnmp.GoSNMP{
		Target:             host.Endpoint,
		Port:               uint16(port),
		Timeout:            timeout,
		Retries:            snmpRetries,
		MaxOids:            gosnmp.MaxOids,
		Transport:          "udp",
		ExponentialTimeout: true,
	}

	switch host.SNMPVersion {
	case "2c":
		g.Version = gosnmp.Version2c
		g.Community = host.SNMPCommunity
	case "3":
		g.Version = gosnmp.Version3
		sp := &gosnmp.UsmSecurityParameters{
			UserName:                 host.SNMPv3User,
			AuthenticationPassphrase: host.SNMPv3AuthPass,
			PrivacyPassphrase:        host.SNMPv3PrivPass,
		}
		authProto, err := authProtocolFor(host.SNMPv3AuthProto)
		if err != nil {
			return nil, err
		}
		privProto, err := privProtocolFor(host.SNMPv3PrivProto)
		if err != nil {
			return nil, err
		}
		switch {
		case host.SNMPv3AuthPass == "" && host.SNMPv3PrivPass == "":
			g.MsgFlags = gosnmp.NoAuthNoPriv
		case host.SNMPv3PrivPass == "":
			g.MsgFlags = gosnmp.AuthNoPriv
			sp.AuthenticationProtocol = authProto
		default:
			g.MsgFlags = gosnmp.AuthPriv
			sp.AuthenticationProtocol = authProto
			sp.PrivacyProtocol = privProto
		}
		g.SecurityModel = gosnmp.UserSecurityModel
		g.SecurityParameters = sp
	default:
		return nil, fmt.Errorf("unsupported snmp_version %q (want 2c or 3)", host.SNMPVersion)
	}

	if err := g.Connect(); err != nil {
		return nil, err
	}
	return goSNMPSession{g}, nil
}

// goSNMPSession adapts *gosnmp.GoSNMP to the collector's session interface:
// Get unwraps SnmpPacket to its variables, Close tolerates a nil Conn.
type goSNMPSession struct {
	g *gosnmp.GoSNMP
}

func (s goSNMPSession) Get(oids []string) ([]gosnmp.SnmpPDU, error) {
	pkt, err := s.g.Get(oids)
	if err != nil {
		return nil, err
	}
	return pkt.Variables, nil
}

func (s goSNMPSession) BulkWalkAll(root string) ([]gosnmp.SnmpPDU, error) {
	return s.g.BulkWalkAll(root)
}

func (s goSNMPSession) Close() error {
	if s.g.Conn == nil {
		return nil
	}
	return s.g.Conn.Close()
}

func authProtocolFor(name string) (gosnmp.SnmpV3AuthProtocol, error) {
	switch name {
	case "MD5":
		return gosnmp.MD5, nil
	case "", "SHA":
		return gosnmp.SHA, nil
	case "SHA224":
		return gosnmp.SHA224, nil
	case "SHA256":
		return gosnmp.SHA256, nil
	case "SHA384":
		return gosnmp.SHA384, nil
	case "SHA512":
		return gosnmp.SHA512, nil
	default:
		return 0, fmt.Errorf("unsupported snmp_v3_auth_proto %q", name)
	}
}

func privProtocolFor(name string) (gosnmp.SnmpV3PrivProtocol, error) {
	switch name {
	case "DES":
		return gosnmp.DES, nil
	case "", "AES":
		return gosnmp.AES, nil
	case "AES192":
		return gosnmp.AES192C, nil
	case "AES256":
		return gosnmp.AES256C, nil
	default:
		return 0, fmt.Errorf("unsupported snmp_v3_priv_proto %q", name)
	}
}
