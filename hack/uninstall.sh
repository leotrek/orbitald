#!/bin/sh
set -eu

PREFIX=${PREFIX:-/usr/local}
BINDIR=${BINDIR:-"$PREFIX/bin"}
CLIBINDIR=${CLIBINDIR:-"$BINDIR"}
UNITDIR=${UNITDIR:-"$PREFIX/lib/systemd/system"}
DESTDIR=${DESTDIR:-}
ORBITALD_STATE_DIR=${ORBITALD_STATE_DIR:-/var/lib/orbitald}
REMOVE_CONTAINERD=${REMOVE_CONTAINERD:-ask}
PURGE_STATE=${PURGE_STATE:-0}
SYSTEMD_RELOAD=${SYSTEMD_RELOAD:-1}
STOP_SERVICE=${STOP_SERVICE:-1}

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
		1|true|yes|on) return 0 ;;
		*) return 1 ;;
	esac
}

is_falsy() {
	case "$1" in
		0|false|no|off|skip) return 0 ;;
		*) return 1 ;;
	esac
}

command_exists() {
	command -v "$1" >/dev/null 2>&1
}

require_root() {
	if [ "$(id -u)" -ne 0 ]; then
		die "system uninstall requires root; run 'sudo make uninstall' or set DESTDIR to remove staged files only"
	fi
}

require_linux() {
	if [ "$(uname -s)" != "Linux" ]; then
		die "system uninstall is only supported on Linux; set DESTDIR to remove staged files only"
	fi
}

remove_file_if_present() {
	path=$1
	if [ -e "$path" ] || [ -L "$path" ]; then
		log "Removing $path"
		rm -f "$path"
	else
		log "Not present: $path"
	fi
}

remove_dir_if_empty() {
	path=$1
	if [ -d "$path" ]; then
		rmdir "$path" 2>/dev/null || true
	fi
}

stop_orbitald_service() {
	is_truthy "$STOP_SERVICE" || return
	command_exists systemctl || return

	log "Stopping and disabling orbitald.service"
	if ! systemctl disable --now orbitald.service; then
		warn "could not disable/stop orbitald.service; continuing uninstall"
	fi
}

reload_systemd() {
	is_truthy "$SYSTEMD_RELOAD" || return
	command_exists systemctl || return

	log "Reloading systemd units"
	if ! systemctl daemon-reload; then
		warn "systemctl daemon-reload failed"
	fi
}

remove_installed_files() {
	remove_file_if_present "${DESTDIR}${UNITDIR}/orbitald.service"
	remove_file_if_present "${DESTDIR}${BINDIR}/orbitald"
	remove_file_if_present "${DESTDIR}${CLIBINDIR}/obd"

	remove_dir_if_empty "${DESTDIR}${UNITDIR}"
	remove_dir_if_empty "${DESTDIR}${BINDIR}"
	if [ "${DESTDIR}${CLIBINDIR}" != "${DESTDIR}${BINDIR}" ]; then
		remove_dir_if_empty "${DESTDIR}${CLIBINDIR}"
	fi
}

remove_legacy_containerd_dropin() {
	[ -z "$DESTDIR" ] || return

	dropin_file=/etc/systemd/system/containerd.service.d/orbitald-socket-permissions.conf
	remove_file_if_present "$dropin_file"
	remove_dir_if_empty /etc/systemd/system/containerd.service.d
}

purge_state_dir() {
	if is_truthy "$PURGE_STATE"; then
		case "$ORBITALD_STATE_DIR" in
			""|"/"|"/var"|"/usr"|"/usr/local"|"/home")
				die "refusing to purge unsafe ORBITALD_STATE_DIR '$ORBITALD_STATE_DIR'"
				;;
		esac
		if [ -d "$ORBITALD_STATE_DIR" ]; then
			log "Removing state directory $ORBITALD_STATE_DIR"
			rm -rf "$ORBITALD_STATE_DIR"
		else
			log "State directory not present: $ORBITALD_STATE_DIR"
		fi
	else
		log "Preserving state directory $ORBITALD_STATE_DIR"
	fi
}

remove_containerd_package() {
	if command_exists apt-get; then
		log "Removing containerd with apt-get"
		DEBIAN_FRONTEND=noninteractive apt-get remove -y containerd
	elif command_exists dnf; then
		log "Removing containerd with dnf"
		dnf remove -y containerd
	elif command_exists yum; then
		log "Removing containerd with yum"
		yum remove -y containerd
	elif command_exists zypper; then
		log "Removing containerd with zypper"
		zypper --non-interactive remove containerd
	elif command_exists pacman; then
		log "Removing containerd with pacman"
		pacman -R --noconfirm containerd
	elif command_exists apk; then
		log "Removing containerd with apk"
		apk del containerd
	else
		die "containerd removal requested, but no supported package manager was found"
	fi
}

should_remove_containerd() {
	case "$REMOVE_CONTAINERD" in
		ask)
			if [ ! -t 0 ]; then
				warn "not prompting for containerd removal because stdin is not interactive; set REMOVE_CONTAINERD=1 to remove it"
				return 1
			fi
			printf 'Remove containerd package too? This may affect other workloads. [y/N] '
			read answer || answer=
			case "$answer" in
				y|Y|yes|YES) return 0 ;;
				*) return 1 ;;
			esac
			;;
		*)
			if is_truthy "$REMOVE_CONTAINERD"; then
				return 0
			fi
			if is_falsy "$REMOVE_CONTAINERD"; then
				return 1
			fi
			die "unsupported REMOVE_CONTAINERD value '$REMOVE_CONTAINERD'"
			;;
	esac
}

maybe_remove_containerd() {
	[ -z "$DESTDIR" ] || return

	if should_remove_containerd; then
		remove_containerd_package
		return
	fi

	log "Keeping containerd installed"
}

main() {
	log "Starting orbitald uninstall"

	if [ -n "$DESTDIR" ]; then
		log "DESTDIR is set; removing staged files only"
		remove_installed_files
		log "orbitald staged files removed from $DESTDIR"
		return
	fi

	require_linux
	require_root
	stop_orbitald_service
	remove_installed_files
	remove_legacy_containerd_dropin
	reload_systemd
	purge_state_dir
	maybe_remove_containerd

	log "orbitald uninstalled"
}

main "$@"
