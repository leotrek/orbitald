#!/bin/sh
set -eu

PREFIX=${PREFIX:-/usr/local}
BINDIR=${BINDIR:-"$PREFIX/bin"}
CLIBINDIR=${CLIBINDIR:-"$BINDIR"}
UNITDIR=${UNITDIR:-"$PREFIX/lib/systemd/system"}
DESTDIR=${DESTDIR:-}
INSTALL=${INSTALL:-install}
INSTALL_DEPS=${INSTALL_DEPS:-auto}
ORBITALD_USER=${ORBITALD_USER:-}
ORBITALD_GROUP=${ORBITALD_GROUP:-}
ORBITALD_STATE_DIR=${ORBITALD_STATE_DIR:-/var/lib/orbitald}
ORBITALD_SNAPSHOTTER=${ORBITALD_SNAPSHOTTER:-overlayfs}
CONTAINERD_SOCK=${CONTAINERD_SOCK:-/run/containerd/containerd.sock}
CONTAINERD_GROUP=${CONTAINERD_GROUP:-containerd}
CONFIGURE_CONTAINERD_SOCKET_PERMS=${CONFIGURE_CONTAINERD_SOCKET_PERMS:-1}
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

group_exists() {
	if command_exists getent; then
		getent group "$1" >/dev/null 2>&1
	else
		grep -q "^$1:" /etc/group
	fi
}

user_exists() {
	id "$1" >/dev/null 2>&1
}

resolve_orbitald_identity() {
	if [ -z "$ORBITALD_USER" ]; then
		if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
			ORBITALD_USER=$SUDO_USER
		else
			ORBITALD_USER=$(id -un)
		fi
	fi

	if ! user_exists "$ORBITALD_USER"; then
		die "user '$ORBITALD_USER' does not exist; create it first or pass ORBITALD_USER=<existing-user>"
	fi

	if [ -z "$ORBITALD_GROUP" ]; then
		ORBITALD_GROUP=$(id -gn "$ORBITALD_USER") || die "could not resolve primary group for '$ORBITALD_USER'"
	fi

	if ! group_exists "$ORBITALD_GROUP"; then
		die "group '$ORBITALD_GROUP' does not exist; pass ORBITALD_GROUP=<existing-group>"
	fi

	log "Using existing user '$ORBITALD_USER' and group '$ORBITALD_GROUP'"
}

create_group_if_missing() {
	group=$1
	if group_exists "$group"; then
		log "Group '$group' already exists"
		return
	fi

	log "Creating system group '$group'"
	if command_exists groupadd; then
		groupadd --system "$group"
	elif command_exists addgroup; then
		addgroup -S "$group"
	else
		die "no supported group creation command found"
	fi
}

user_in_group() {
	id -nG "$1" 2>/dev/null | tr ' ' '\n' | grep -qx "$2"
}

add_user_to_group() {
	user=$1
	group=$2

	if user_in_group "$user" "$group"; then
		log "User '$user' already belongs to group '$group'"
		return
	fi

	log "Adding user '$user' to group '$group'"
	if command_exists usermod; then
		usermod -aG "$group" "$user"
	elif command_exists addgroup; then
		addgroup "$user" "$group"
	else
		die "no supported group membership command found"
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
	unit_user=$(printf '%s\n' "$ORBITALD_USER" | sed 's/[&#]/\\&/g')
	unit_group=$(printf '%s\n' "$ORBITALD_GROUP" | sed 's/[&#]/\\&/g')
	unit_containerd_sock=$(printf '%s\n' "$CONTAINERD_SOCK" | sed 's/[&#]/\\&/g')
	unit_containerd_group=$(printf '%s\n' "$CONTAINERD_GROUP" | sed 's/[&#]/\\&/g')

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
		-e "s#@CONTAINERD_GROUP@#${unit_containerd_group}#g" \
		-e "s#/usr/local/bin/orbitald#${unit_bindir}/orbitald#g" \
		-e "s#/var/lib/orbitald#${unit_state_dir}#g" \
		"$SERVICE_SOURCE" > "$tmp_unit"
	replace_unit_setting "$tmp_unit" User "$ORBITALD_USER"
	replace_unit_setting "$tmp_unit" Group "$ORBITALD_GROUP"
	replace_unit_setting "$tmp_unit" SupplementaryGroups "$CONTAINERD_GROUP"
	"$INSTALL" -m 0644 "$tmp_unit" "${DESTDIR}${UNITDIR}/orbitald.service"
	rm -f "$tmp_unit"
}

replace_unit_setting() {
	file=$1
	key=$2
	value=$3

	[ -n "$value" ] || return 0

	escaped_value=$(printf '%s\n' "$value" | sed 's/[&#]/\\&/g')
	tmp_next=$(mktemp)
	sed -e "s#^${key}=.*#${key}=${escaped_value}#g" "$file" > "$tmp_next"
	mv "$tmp_next" "$file"
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
	chown "$ORBITALD_USER:$ORBITALD_GROUP" "$path" || die "could not set owner on $path to $ORBITALD_USER:$ORBITALD_GROUP"
	chmod "$mode" "$path" || die "could not set mode $mode on $path"
}

install_state_dir() {
	validate_state_dir

	log "Creating state directory $ORBITALD_STATE_DIR"
	"$INSTALL" -d -m 0750 -o "$ORBITALD_USER" -g "$ORBITALD_GROUP" "$ORBITALD_STATE_DIR" || die "could not create $ORBITALD_STATE_DIR"
	"$INSTALL" -d -m 0750 -o "$ORBITALD_USER" -g "$ORBITALD_GROUP" "$ORBITALD_STATE_DIR/runs" || die "could not create $ORBITALD_STATE_DIR/runs"
	"$INSTALL" -d -m 0700 -o "$ORBITALD_USER" -g "$ORBITALD_GROUP" "$ORBITALD_STATE_DIR/.docker" || die "could not create $ORBITALD_STATE_DIR/.docker"

	log "Ensuring required state paths are owned by $ORBITALD_USER:$ORBITALD_GROUP"
	set_state_path_owner_mode "$ORBITALD_STATE_DIR" 0750
	set_state_path_owner_mode "$ORBITALD_STATE_DIR/runs" 0750
	set_state_path_owner_mode "$ORBITALD_STATE_DIR/.docker" 0700
	set_state_path_owner_mode "$ORBITALD_STATE_DIR/state.json" 0640
}

install_containerd_dropin() {
	is_truthy "$CONFIGURE_CONTAINERD_SOCKET_PERMS" || return
	command_exists systemctl || return
	[ -d /etc/systemd/system ] || return

	chgrp_path=$(command -v chgrp || true)
	chmod_path=$(command -v chmod || true)
	if [ -z "$chgrp_path" ] || [ -z "$chmod_path" ]; then
		warn "cannot create containerd permission drop-in because chgrp/chmod was not found"
		return
	fi

	dropin_dir=/etc/systemd/system/containerd.service.d
	dropin_file=$dropin_dir/orbitald-socket-permissions.conf
	log "Installing containerd socket permission drop-in at $dropin_file"
	"$INSTALL" -d -m 0755 "$dropin_dir"
	tmp_dropin=$(mktemp)
	{
		printf '[Service]\n'
		printf 'ExecStartPost=%s %s %s\n' "$chgrp_path" "$CONTAINERD_GROUP" "$CONTAINERD_SOCK"
		printf 'ExecStartPost=%s 0660 %s\n' "$chmod_path" "$CONTAINERD_SOCK"
	} > "$tmp_dropin"
	"$INSTALL" -m 0644 "$tmp_dropin" "$dropin_file"
	rm -f "$tmp_dropin"
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

set_current_socket_permissions() {
	[ -S "$CONTAINERD_SOCK" ] || {
		warn "containerd socket not found at $CONTAINERD_SOCK; orbitald will need access before it can run functions"
		return
	}

	log "Setting containerd socket permissions on $CONTAINERD_SOCK"
	if ! chgrp "$CONTAINERD_GROUP" "$CONTAINERD_SOCK"; then
		warn "could not set containerd socket group to '$CONTAINERD_GROUP'"
	fi
	if ! chmod 0660 "$CONTAINERD_SOCK"; then
		warn "could not set containerd socket mode to 0660"
	fi
}

check_orbitald_socket_access() {
	[ -S "$CONTAINERD_SOCK" ] || return
	command_exists runuser || {
		warn "runuser not found; skipping containerd socket access check for '$ORBITALD_USER'"
		return
	}

	if runuser -u "$ORBITALD_USER" -- test -r "$CONTAINERD_SOCK" -a -w "$CONTAINERD_SOCK"; then
		log "Verified '$ORBITALD_USER' can access $CONTAINERD_SOCK"
	else
		warn "'$ORBITALD_USER' cannot access $CONTAINERD_SOCK yet; inspect socket owner, group, and mode"
	fi
}

main() {
	log "Starting orbitald install"

	if [ -n "$DESTDIR" ]; then
		log "DESTDIR is set; staging files only and skipping host dependency/user setup"
		if [ -z "$ORBITALD_USER" ] || [ -z "$ORBITALD_GROUP" ]; then
			warn "leaving User/Group placeholders in staged unit; pass ORBITALD_USER and ORBITALD_GROUP to generate concrete values"
		fi
		install_files
		log "orbitald files staged under $DESTDIR"
		return
	fi

	require_linux
	require_root
	ensure_containerd_installed
	resolve_orbitald_identity
	create_group_if_missing "$CONTAINERD_GROUP"
	add_user_to_group "$ORBITALD_USER" "$CONTAINERD_GROUP"
	install_files
	install_state_dir
	install_containerd_dropin
	reload_systemd
	start_containerd
	set_current_socket_permissions
	check_orbitald_socket_access

	log "orbitald installed"
	log "Enable it with: systemctl enable --now orbitald"
}

main "$@"
