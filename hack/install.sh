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

validate_state_dir() {
	case "$ORBITALD_STATE_DIR" in
		/*) ;;
		*) die "ORBITALD_STATE_DIR must be an absolute path: $ORBITALD_STATE_DIR" ;;
	esac

	case "$ORBITALD_STATE_DIR" in
		/|/home|/private|/private/tmp|/srv|/tmp|/usr|/usr/local|/var|/var/lib)
			die "refusing to manage unsafe ORBITALD_STATE_DIR '$ORBITALD_STATE_DIR'"
			;;
	esac

	if [ -L "$ORBITALD_STATE_DIR" ]; then
		die "refusing to manage symlinked ORBITALD_STATE_DIR '$ORBITALD_STATE_DIR'"
	fi
}

set_state_path_owner_mode() {
	path=$1
	mode=$2

	[ -e "$path" ] || return
	chown root:root "$path" || die "could not set owner on $path to root:root"
	chmod "$mode" "$path" || die "could not set mode $mode on $path"
}

install_state_dir() {
	validate_state_dir

	log "Creating state directory $ORBITALD_STATE_DIR"
	"$INSTALL" -d -m 0750 -o root -g root "$ORBITALD_STATE_DIR" || die "could not create $ORBITALD_STATE_DIR"
	"$INSTALL" -d -m 0750 -o root -g root "$ORBITALD_STATE_DIR/runs" || die "could not create $ORBITALD_STATE_DIR/runs"
	"$INSTALL" -d -m 0700 -o root -g root "$ORBITALD_STATE_DIR/.docker" || die "could not create $ORBITALD_STATE_DIR/.docker"

	log "Ensuring required state paths are owned by root:root"
	set_state_path_owner_mode "$ORBITALD_STATE_DIR" 0750
	set_state_path_owner_mode "$ORBITALD_STATE_DIR/runs" 0750
	set_state_path_owner_mode "$ORBITALD_STATE_DIR/.docker" 0700
	set_state_path_owner_mode "$ORBITALD_STATE_DIR/state.json" 0640
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

remove_legacy_containerd_dropin() {
	[ -z "$DESTDIR" ] || return

	dropin_file=/etc/systemd/system/containerd.service.d/orbitald-socket-permissions.conf
	if [ -e "$dropin_file" ] || [ -L "$dropin_file" ]; then
		log "Removing obsolete containerd socket permission drop-in at $dropin_file"
		rm -f "$dropin_file"
	fi
	rmdir /etc/systemd/system/containerd.service.d 2>/dev/null || true
}

main() {
	log "Starting orbitald install"

	if [ -n "$DESTDIR" ]; then
		log "DESTDIR is set; staging files only and skipping host dependency/user setup"
		install_files
		log "orbitald files staged under $DESTDIR"
		return
	fi

	require_linux
	require_root
	ensure_containerd_installed
	install_files
	install_state_dir
	remove_legacy_containerd_dropin
	reload_systemd
	start_containerd

	log "orbitald installed"
	log "Enable it with: systemctl enable --now orbitald"
}

main "$@"
