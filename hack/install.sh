#!/bin/sh
set -eu

PREFIX=${PREFIX:-/usr/local}
BINDIR=${BINDIR:-"$PREFIX/bin"}
CLIBINDIR=${CLIBINDIR:-"$BINDIR"}
UNITDIR=${UNITDIR:-"$PREFIX/lib/systemd/system"}
DESTDIR=${DESTDIR:-}
INSTALL=${INSTALL:-install}
INSTALL_DEPS=${INSTALL_DEPS:-auto}
ORBITALD_STATE_DIR=${ORBITALD_STATE_DIR:-/var/lib/orbitald}
ORBITALD_SNAPSHOTTER=${ORBITALD_SNAPSHOTTER:-overlayfs}
CONTAINERD_SOCK=${CONTAINERD_SOCK:-/run/containerd/containerd.sock}
START_CONTAINERD=${START_CONTAINERD:-1}
SYSTEMD_RELOAD=${SYSTEMD_RELOAD:-1}
BINARY_SOURCE=${BINARY_SOURCE:-bin/orbitald}
CLI_SOURCE=${CLI_SOURCE:-bin/obd}
SERVICE_SOURCE=${SERVICE_SOURCE:-hack/orbitald.service}

log() {
	printf 'INFO: %s\n' "$*"
}

warn() {
	printf 'WARN: %s\n' "$*" >&2
}

die() {
	printf 'ERROR: %s\n' "$*" >&2
	exit 1
}

is_truthy() {
	case "$1" in
		1|true|yes|on|auto) return 0 ;;
		*) return 1 ;;
	esac
}

command_exists() {
	command -v "$1" >/dev/null 2>&1
}

require_file() {
	[ -f "$1" ] || die "required file not found: $1"
}

require_root() {
	if [ "$(id -u)" -ne 0 ]; then
		die "system install requires root; run 'sudo make install' or set DESTDIR to stage files only"
	fi
}

require_linux() {
	if [ "$(uname -s)" != "Linux" ]; then
		die "system install is only supported on Linux; set DESTDIR to stage files without host setup"
	fi
}

install_containerd_package() {
	if command_exists apt-get; then
		log "Installing containerd with apt-get"
		apt-get update
		DEBIAN_FRONTEND=noninteractive apt-get install -y containerd
	elif command_exists dnf; then
		log "Installing containerd with dnf"
		dnf install -y containerd
	elif command_exists yum; then
		log "Installing containerd with yum"
		yum install -y containerd
	elif command_exists zypper; then
		log "Installing containerd with zypper"
		zypper --non-interactive install containerd
	elif command_exists pacman; then
		log "Installing containerd with pacman"
		pacman -Sy --noconfirm containerd
	elif command_exists apk; then
		log "Installing containerd with apk"
		apk add --no-cache containerd
	else
		die "containerd is missing and no supported package manager was found"
	fi
}

ensure_containerd_installed() {
	if command_exists containerd; then
		log "containerd is already installed"
		return
	fi

	case "$INSTALL_DEPS" in
		0|false|no|off|skip)
			warn "containerd is not installed and dependency installation is disabled"
			return
			;;
		auto|1|true|yes|on)
			install_containerd_package
			;;
		*)
			die "unsupported INSTALL_DEPS value '$INSTALL_DEPS'"
			;;
	esac
}

install_files() {
	require_file "$BINARY_SOURCE"
	require_file "$CLI_SOURCE"
	require_file "$SERVICE_SOURCE"
	unit_bindir=$(printf '%s\n' "$BINDIR" | sed 's/[&#]/\\&/g')
	unit_state_dir=$(printf '%s\n' "$ORBITALD_STATE_DIR" | sed 's/[&#]/\\&/g')
	unit_snapshotter=$(printf '%s\n' "$ORBITALD_SNAPSHOTTER" | sed 's/[&#]/\\&/g')
	unit_containerd_sock=$(printf '%s\n' "$CONTAINERD_SOCK" | sed 's/[&#]/\\&/g')

	log "Installing orbitald binary to ${DESTDIR}${BINDIR}/orbitald"
	"$INSTALL" -d -m 0755 "${DESTDIR}${BINDIR}"
	"$INSTALL" -m 0755 "$BINARY_SOURCE" "${DESTDIR}${BINDIR}/orbitald"

	log "Installing obd CLI to ${DESTDIR}${CLIBINDIR}/obd"
	"$INSTALL" -d -m 0755 "${DESTDIR}${CLIBINDIR}"
	"$INSTALL" -m 0755 "$CLI_SOURCE" "${DESTDIR}${CLIBINDIR}/obd"

	log "Installing systemd unit to ${DESTDIR}${UNITDIR}/orbitald.service"
	"$INSTALL" -d -m 0755 "${DESTDIR}${UNITDIR}"
	tmp_unit=$(mktemp)
	sed \
		-e "s#@BINDIR@#${unit_bindir}#g" \
		-e "s#@ORBITALD_STATE_DIR@#${unit_state_dir}#g" \
		-e "s#@ORBITALD_SNAPSHOTTER@#${unit_snapshotter}#g" \
		-e "s#@CONTAINERD_SOCK@#${unit_containerd_sock}#g" \
		-e "s#/usr/local/bin/orbitald#${unit_bindir}/orbitald#g" \
		-e "s#/var/lib/orbitald#${unit_state_dir}#g" \
		"$SERVICE_SOURCE" > "$tmp_unit"
	"$INSTALL" -m 0644 "$tmp_unit" "${DESTDIR}${UNITDIR}/orbitald.service"
	rm -f "$tmp_unit"
}

install_state_dir() {
	log "Creating state directory $ORBITALD_STATE_DIR"
	"$INSTALL" -d -m 0750 -o root -g root "$ORBITALD_STATE_DIR" || die "could not create $ORBITALD_STATE_DIR"
	"$INSTALL" -d -m 0750 -o root -g root "$ORBITALD_STATE_DIR/runs" || die "could not create $ORBITALD_STATE_DIR/runs"
}

reload_systemd() {
	is_truthy "$SYSTEMD_RELOAD" || return
	command_exists systemctl || return

	log "Reloading systemd units"
	if ! systemctl daemon-reload; then
		warn "systemctl daemon-reload failed; reload manually before enabling orbitald"
	fi
}

start_containerd() {
	is_truthy "$START_CONTAINERD" || return
	command_exists systemctl || return

	log "Enabling and starting containerd"
	if ! systemctl enable --now containerd; then
		warn "could not enable/start containerd; check the containerd service manually"
	fi
}

main() {
	log "Starting orbitald install"

	if [ -n "$DESTDIR" ]; then
		log "DESTDIR is set; staging files only and skipping host setup"
		install_files
		log "orbitald files staged under $DESTDIR"
		return
	fi

	require_linux
	require_root
	ensure_containerd_installed
	install_files
	install_state_dir
	reload_systemd
	start_containerd

	log "orbitald installed"
	log "Enable it with: systemctl enable --now orbitald"
}

main "$@"
