package scheduler

import (
	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

// CollectorHostFor maps a stored host onto the collector package's decoupled
// host view. It is the single mapping point for every collection path:
// scheduled polling and the dashboard's script test-run.
func CollectorHostFor(host storage.Host) collector.Host {
	return collector.Host{
		ID:                  host.ID,
		Name:                host.Name,
		Connection:          host.Connection,
		Endpoint:            host.Endpoint,
		Port:                host.Port,
		User:                host.User,
		KeyPath:             host.KeyPath,
		Sudo:                host.Sudo,
		Timeout:             host.Timeout,
		SSHTimeout:          durationFromMs(host.SshTimeoutMs),
		CollectorTimeout:    durationFromMs(host.CollectorTimeoutMs),
		SSHHostKeyPolicy:    derefString(host.SSHHostKeyPolicy),
		ProxyJump:           host.ProxyJump,
		CollectorPreference: host.CollectorPreference,
		ScriptName:          host.ScriptName,
		ScriptCommand:       host.ScriptCommand,
		ScriptParse:         host.ScriptParse,
		SNMPVersion:         host.SNMPVersion,
		SNMPCommunity:       host.SNMPCommunity,
		SNMPv3User:          host.SNMPv3User,
		SNMPv3AuthProto:     host.SNMPv3AuthProto,
		SNMPv3AuthPass:      host.SNMPv3AuthPass,
		SNMPv3PrivProto:     host.SNMPv3PrivProto,
		SNMPv3PrivPass:      host.SNMPv3PrivPass,
		SNMPExtraOIDs:       host.SNMPExtraOIDs,
	}
}
