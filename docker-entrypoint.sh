#!/bin/sh
# docker-entrypoint.sh
# Handles optional Tailscale startup before launching the monitor

set -e

# Start Tailscale if auth key provided
if [ -n "$TS_AUTHKEY" ]; then
    echo "Starting Tailscale..."
    tailscaled --tun=userspace-networking --socks5-server=localhost:1055 &
    sleep 2

    # Build tailscale up command
    TS_ARGS="up --authkey=${TS_AUTHKEY} --hostname=${TS_HOSTNAME:-uptime-monitor}"

    if [ -n "$TS_ROUTES" ]; then
        TS_ARGS="${TS_ARGS} --advertise-routes=${TS_ROUTES}"
    fi

    if [ -n "$TS_EXTRA_ARGS" ]; then
        TS_ARGS="${TS_ARGS} ${TS_EXTRA_ARGS}"
    fi

    tailscale ${TS_ARGS}

    echo "Tailscale started. SOCKS5 proxy on localhost:1055"
else
    echo "TS_AUTHKEY not set, skipping Tailscale startup"
fi

# Launch the monitor
exec /app/uptime-monitor "$@"