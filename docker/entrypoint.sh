#!/bin/sh
# Container startup: bring up the WireGuard tunnel, then run unified.
#
# Only traffic sent from the tunnel's own address goes through the tunnel.
# unified binds its torrent sockets to that address, so torrents use the VPN,
# while the web UI, Prowlarr and TMDB use the container's normal network.
set -eu

WG_CONF=/config/wg0.conf
TABLE=51820
PUID=${PUID:-1000}
PGID=${PGID:-1000}

# add_address CIDR: assigns the address to wg0 and remembers it in $bound.
# IPv6 is skipped when the container has it disabled.
add_address() {
	case $1 in
	*:*) family=-6 ;;
	*) family=-4 ;;
	esac
	if ! ip "$family" address add "$1" dev wg0 2>/dev/null; then
		if [ "$family" = -6 ]; then
			echo "IPv6 is not available in this container, skipping $1"
			return
		fi
		echo "cannot add address $1 to wg0" >&2
		exit 1
	fi
	bound="$bound $1"
}

# route_from CIDR: sends traffic from that address into the tunnel. wg0 must
# be up first.
route_from() {
	case $1 in
	*:*) family=-6 ;;
	*) family=-4 ;;
	esac
	ip "$family" rule add from "${1%/*}" table "$TABLE"
	ip "$family" route add default dev wg0 table "$TABLE"
}

if [ -f "$WG_CONF" ]; then
	addresses=$(sed -n 's/^[[:space:]]*Address[[:space:]]*=[[:space:]]*//p' "$WG_CONF" | tr ',' ' ')
	if [ -z "$addresses" ]; then
		echo "$WG_CONF has no Address line" >&2
		exit 1
	fi
	# wg only accepts WireGuard's own keys; Address, DNS and the others are
	# wg-quick additions, handled above or not needed.
	sed -E '/^[[:space:]]*(Address|DNS|MTU|Table|PreUp|PostUp|PreDown|PostDown|SaveConfig)[[:space:]]*=/d' "$WG_CONF" >/tmp/wg0.conf

	ip link delete wg0 2>/dev/null || true
	if ! ip link add wg0 type wireguard; then
		echo "cannot create a WireGuard interface: give the container NET_ADMIN, and make sure the host kernel has WireGuard (modprobe wireguard)" >&2
		exit 1
	fi
	wg setconf wg0 /tmp/wg0.conf
	rm /tmp/wg0.conf
	bound=""
	for a in $addresses; do
		add_address "$a"
	done
	ip link set wg0 mtu 1420 up
	for a in $bound; do
		route_from "$a"
	done
	echo "WireGuard tunnel wg0 is up:$bound"
	export UNIFIED_VPN_INTERFACE=wg0
elif [ "${VPN:-on}" = off ]; then
	echo "VPN=off: torrent traffic uses the container's normal network"
else
	echo "No WireGuard config at $WG_CONF. Put your Mullvad config there, or set VPN=off to run without a VPN." >&2
	exit 1
fi

if [ ! -f "$UNIFIED_CONFIG" ]; then
	unified init
fi

# Run unprivileged. The WireGuard config (with its private key) stays
# readable by root only.
# Docker creates a missing volume folder owned by root; hand such an empty
# folder over. A folder with content, like a NAS share, is left as it is.
if [ "$(stat -c %u /media)" = 0 ] && [ -z "$(ls -A /media)" ]; then
	chown "$PUID:$PGID" /media
fi
mkdir -p "$UNIFIED_DATA_DIR"
chown "$PUID:$PGID" /config "$UNIFIED_CONFIG"
chown -R "$PUID:$PGID" "$UNIFIED_DATA_DIR"
exec su-exec "$PUID:$PGID" unified serve --addr 0.0.0.0:8080
