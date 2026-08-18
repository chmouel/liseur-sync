#!/usr/bin/env bash
# install.sh — interactive installer for liseur-sync.
#
# https://github.com/chmouel/liseur-sync
#
# Picks a runtime (Docker Compose, or rootless Podman + a systemd user
# quadlet), a database (SQLite or bundled PostgreSQL), starts the
# server, and walks you through creating the first user and device
# credentials. Safe to re-run; existing installs are detected.
#
# Environment knobs:
#   LISEUR_VERSION   image tag to deploy            (default: latest)
#   LISEUR_REF       git ref to fetch compose.yaml  (default: main)
#   NO_COLOR         disable colored output
#
# Non-interactive use:
#   ./install.sh --yes --db=sqlite --runtime=docker --port=8585
set -euo pipefail

REPO="chmouel/liseur-sync"
IMAGE_REPO="ghcr.io/chmouel/liseur-sync"
VERSION="${LISEUR_VERSION:-latest}"
GIT_REF="${LISEUR_REF:-main}"
COMPOSE_URL="${LISEUR_COMPOSE_URL:-https://raw.githubusercontent.com/${REPO}/${GIT_REF}/compose.yaml}"
APP_BIN=/ko-app/liseur-sync # binary path inside the ko-built image

# ---------------------------------------------------------------- ui layer
# gum is used when available and interactive; otherwise plain read/echo.
USE_GUM=0
if command -v gum >/dev/null 2>&1 && [ -t 0 ] && [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	USE_GUM=1
fi

_c_reset=$'\033[0m' _c_dim=$'\033[2m' _c_red=$'\033[31m' _c_grn=$'\033[32m'
_c_ylw=$'\033[33m' _c_blu=$'\033[34m' _c_bold=$'\033[1m'
if [ -n "${NO_COLOR:-}" ] || [ ! -t 1 ]; then
	_c_reset='' _c_dim='' _c_red='' _c_grn='' _c_ylw='' _c_blu='' _c_bold=''
fi

ui_phase() { # ui_phase "3/6" "Install"
	if [ "$USE_GUM" = 1 ]; then
		gum style --border rounded --border-foreground 12 --padding "0 2" --margin "1 0" \
			--bold --foreground 15 " ${1} · ${2} "
	else
		printf '\n%s== %s · %s ==%s\n' "$_c_bold$_c_blu" "$1" "$2" "$_c_reset"
	fi
}

ui_info() { printf '%s[*]%s %s\n' "$_c_blu" "$_c_reset" "$*"; }
ui_ok() { printf '%s[✓]%s %s\n' "$_c_grn" "$_c_reset" "$*"; }
ui_warn() { printf '%s[!]%s %s\n' "$_c_ylw" "$_c_reset" "$*" >&2; }
ui_err() { printf '%s[✗]%s %s\n' "$_c_red" "$_c_reset" "$*" >&2; }
ui_dim() { printf '%s    %s%s\n' "$_c_dim" "$*" "$_c_reset"; }

# ui_choose "header" default_index opt... -> prints 1-based choice index
ui_choose() {
	local header="$1" def="$2"
	shift 2
	if [ "$USE_GUM" = 1 ]; then
		local picked i=1 opt
		picked=$(gum choose --header "$header" --cursor-prefix "→ " \
			--selected-prefix "● " --unselected-prefix "○ " "$@") || die "aborted"
		tty_restore
		for opt in "$@"; do
			if [ "$opt" = "$picked" ]; then
				echo "$i"
				return 0
			fi
			i=$((i + 1))
		done
		echo "$def"
		return 0
	fi
	# menu goes to stderr; only the chosen index is printed on stdout
	printf '%s%s%s\n' "$_c_bold" "$header" "$_c_reset" >&2
	local i=1 opt
	for opt in "$@"; do
		if [ "$i" = "$def" ]; then
			printf '  %s%d)%s %s %s(default)%s\n' "$_c_bold" "$i" "$_c_reset" "$opt" "$_c_dim" "$_c_reset" >&2
		else
			printf '   %d) %s\n' "$i" "$opt" >&2
		fi
		i=$((i + 1))
	done
	local ans
	while true; do
		tty_read ans "$(printf 'Choice [%s]: ' "$def")" || die "aborted"
		[ -z "$ans" ] && ans="$def"
		if [[ "$ans" =~ ^[0-9]+$ ]] && [ "$ans" -ge 1 ] && [ "$ans" -le $# ]; then
			echo "$ans"
			return 0
		fi
		ui_warn "enter a number between 1 and $#"
	done
}

# ui_confirm "question" [default_yes] -> 0 yes / 1 no
ui_confirm() {
	local q="$1" def="${2:-1}" rc=0
	if [ "$USE_GUM" = 1 ]; then
		if [ "$def" = 1 ]; then
			gum confirm --affirmative "Yes" --negative "No" "$q" </dev/tty || rc=$?
		else
			gum confirm --affirmative "Yes" --negative "No" --default=false "$q" </dev/tty || rc=$?
		fi
		tty_restore
		return "$rc"
	fi
	local hint="[Y/n]" ans
	[ "$def" = 0 ] && hint="[y/N]"
	while true; do
		tty_read ans "$q $hint " || die "aborted"
		case "${ans:-}" in
		"") return "$((1 - def))" ;;
		[yY] | [yY][eE][sS]) return 0 ;;
		[nN] | [nN][oO]) return 1 ;;
		*) ui_warn "answer y or n" ;;
		esac
	done
}

# ui_input "prompt" "default" -> prints value. Empty input yields the
# default; gum shows it as a placeholder ghost, not a prefilled value
# (--value would append typed text to the default instead).
ui_input() {
	local prompt="$1" def="$2" ans
	if [ "$USE_GUM" = 1 ]; then
		ans=$(gum input --header "$prompt" --placeholder "$def" </dev/tty) || die "aborted"
		tty_restore
		printf '%s\n' "${ans:-$def}"
		return
	fi
	tty_read ans "$prompt [$def]: " || die "aborted"
	printf '%s\n' "${ans:-$def}"
}

# ui_spin "title" cmd... -> runs cmd with a spinner (gum) or plainly
ui_spin() {
	local title="$1"
	shift
	if [ "$USE_GUM" = 1 ]; then
		gum spin --spinner dot --title "$title" -- "$@"
	else
		ui_info "$title"
		"$@"
	fi
}

# ui_summary: boxed recap at the end. Reads lines from stdin.
ui_summary() {
	local body
	body=$(cat)
	if [ "$USE_GUM" = 1 ]; then
		printf '%s' "$body" | gum style --border double --border-foreground 10 \
			--padding "1 3" --margin "1 0"
	else
		printf '\n%s────────────────────────────────────────%s\n' "$_c_grn" "$_c_reset"
		printf '%s\n' "$body"
		printf '%s────────────────────────────────────────%s\n' "$_c_grn" "$_c_reset"
	fi
}

die() {
	ui_err "$*"
	exit 1
}

# Bubble Tea (gum's TUI) leaves the terminal in raw mode when its output
# is not itself a terminal; a later `docker exec </dev/tty` would then
# never see a line delimiter and hang. Restore cooked mode after gum.
tty_restore() {
	[ "$USE_GUM" = 1 ] && stty sane </dev/tty 2>/dev/null || true
}

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not installed. $2"
}

# tty_read var prompt -> read a line into var from the terminal when
# available, else stdin. (SC2229: indirect read is intentional.)
# shellcheck disable=SC2229
tty_read() {
	local __var="$1" prompt="$2"
	# Probe usability: permission bits can say rw while no controlling
	# terminal exists (e.g. piped CI shells).
	if (exec 3<>/dev/tty) 2>/dev/null; then
		exec 3>&-
		printf '%s' "$prompt" >/dev/tty
		IFS= read -r "$__var" </dev/tty
	else
		# prompt to stderr so it survives stdout capture in $()
		printf '%s' "$prompt" >&2
		IFS= read -r "$__var"
	fi
}

# ---------------------------------------------------------------- helpers

rand_hex() { # url-safe random secret
	if command -v openssl >/dev/null 2>&1; then
		openssl rand -hex 24
	else
		od -An -N24 -tx1 /dev/urandom | tr -d ' \n'
	fi
}

version_ge() { # version_ge "5.1" "4.4" -> 0 if $1 >= $2
	[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

# ---------------------------------------------------------------- CLI args

ASSUME_YES=0
ARG_DB="" ARG_RUNTIME="" ARG_PORT="" ARG_DIR=""

usage() {
	cat <<EOF
liseur-sync installer

usage: install.sh [options]

  --yes                accept all defaults, never prompt
  --db=sqlite|postgres database backend        (default: ask)
  --runtime=docker|podman   container runtime  (default: ask/detect)
  --port=N             host port               (default: 8585)
  --install-dir=PATH   docker-mode install dir (default: ~/.local/share/liseur-sync)
  -h, --help           this help

env: LISEUR_VERSION (image tag, default latest), LISEUR_REF (compose source, default main)
     LISEUR_COMPOSE_URL (full override for the compose.yaml URL)
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--yes | -y) ASSUME_YES=1 ;;
	--db=*) ARG_DB="${1#*=}" ;;
	--runtime=*) ARG_RUNTIME="${1#*=}" ;;
	--port=*) ARG_PORT="${1#*=}" ;;
	--install-dir=*) ARG_DIR="${1#*=}" ;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "unknown option: $1 (try --help)" ;;
	esac
	shift
done

# In --yes mode there must be nothing to read from.
if [ "$ASSUME_YES" = 1 ]; then
	USE_GUM=0
fi

# asked -> 0 when running interactively (prompts allowed)
asked() { [ "$ASSUME_YES" = 0 ] && [ -t 0 ]; }

# ---------------------------------------------------------------- phases

OS="" DISTRO="" SUDO="" PORT="" DB="" RUNTIME="" INSTALL_DIR=""
HAVE_DOCKER=0 HAVE_COMPOSE=0 HAVE_PODMAN=0 PODMAN_VER=""

preflight() {
	ui_phase "1/6" "Preflight"
	OS=$(uname -s)
	case "$OS" in
	Linux) ;;
	Darwin)
		# Docker Desktop only; no systemd, so the podman path is out.
		ui_info "macOS detected — will use Docker Compose (requires Docker Desktop)."
		;;
	*)
		die "unsupported OS: $OS. Supported: Linux (docker or podman+systemd) and macOS (Docker Desktop)."
		;;
	esac

	if [ -r /etc/os-release ]; then
		# shellcheck disable=SC1091
		DISTRO=$(. /etc/os-release && echo "${ID:-unknown}")
	fi
	need_cmd curl "Install it with your package manager."
	need_cmd sed "Install it with your package manager."

	if [ "$(id -u)" -eq 0 ]; then
		SUDO=""
	elif command -v sudo >/dev/null 2>&1; then
		SUDO="sudo"
	else
		SUDO=""
	fi

	if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
		HAVE_DOCKER=1
		if docker compose version >/dev/null 2>&1 || command -v docker-compose >/dev/null 2>&1; then
			HAVE_COMPOSE=1
		fi
	fi
	if command -v podman >/dev/null 2>&1; then
		HAVE_PODMAN=1
		PODMAN_VER=$(podman version --format '{{.Client.Version}}' 2>/dev/null || echo "0")
	fi

	ui_ok "os: ${OS}${DISTRO:+ ($DISTRO)}, arch: $(uname -m)"
	[ "$HAVE_DOCKER" = 1 ] && ui_ok "docker: $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo present)"
	[ "$HAVE_PODMAN" = 1 ] && ui_ok "podman: ${PODMAN_VER}"
	return 0
}

choose_db() {
	ui_phase "2/6" "Database"
	if [ -n "$ARG_DB" ]; then
		DB="$ARG_DB"
	elif asked; then
		local c
		c=$(ui_choose "Which database should liseur-sync use?" 1 \
			"SQLite — single file, zero extra services (recommended)" \
			"PostgreSQL — bundled postgres:17 container alongside the app")
		[ "$c" = 1 ] && DB="sqlite" || DB="postgres"
	else
		DB="sqlite"
	fi
	case "$DB" in sqlite | postgres) ;; *) die "--db must be sqlite or postgres" ;; esac
	ui_ok "database: $DB"
}

install_podman_pkg() {
	ui_info "installing podman…"
	case "$DISTRO" in
	debian | ubuntu | linuxmint | pop)
		$SUDO apt-get update -qq && $SUDO apt-get install -y podman
		;;
	fedora)
		$SUDO dnf install -y podman
		;;
	rhel | centos | rocky | alma)
		$SUDO dnf install -y podman || $SUDO yum install -y podman
		;;
	arch | manjaro | endeavouros | cachyos)
		$SUDO pacman -S --needed --noconfirm podman
		;;
	opensuse* | sles)
		$SUDO zypper install -y podman
		;;
	*)
		return 1
		;;
	esac
}

choose_runtime() {
	ui_phase "3/6" "Runtime"
	if [ -n "$ARG_RUNTIME" ]; then
		RUNTIME="$ARG_RUNTIME"
	elif [ "$HAVE_DOCKER" = 1 ] && [ "$HAVE_COMPOSE" = 1 ] && [ "$OS" = "Linux" ] && asked; then
		local c
		c=$(ui_choose "Docker detected — how do you want to run liseur-sync?" 1 \
			"Docker Compose — standard multi-container setup (recommended)" \
			"Podman + systemd user service — rootless, no daemon")
		[ "$c" = 1 ] && RUNTIME="docker" || RUNTIME="podman"
	elif [ "$HAVE_DOCKER" = 1 ] && [ "$HAVE_COMPOSE" = 1 ]; then
		RUNTIME="docker"
	elif [ "$OS" = "Linux" ]; then
		RUNTIME="podman"
	else
		die "no Docker found. On macOS install Docker Desktop first: https://docs.docker.com/get-docker/"
	fi

	case "$RUNTIME" in
	docker)
		if [ "$HAVE_DOCKER" != 1 ] || [ "$HAVE_COMPOSE" != 1 ]; then
			die "docker runtime selected but Docker (with the compose plugin) is not available."
		fi
		;;
	podman)
		[ "$OS" = "Linux" ] ||
			die "the podman+systemd path requires Linux (no systemd on $OS). Use --runtime=docker."
		;;
	*)
		die "--runtime must be docker or podman"
		;;
	esac

	if [ "$RUNTIME" = "podman" ] && [ "$HAVE_PODMAN" != 1 ]; then
		ui_warn "podman is not installed."
		# Installing a system package always requires explicit consent —
		# never auto-install in --yes mode.
		if ! asked || ! ui_confirm "Install podman now?" 1; then
			die "podman is required for this path. See https://podman.io/docs/installation"
		fi
		install_podman_pkg || {
			ui_warn "no package-manager rule for distro '${DISTRO:-unknown}'; trying the official installer."
			curl -fsSL https://get.podman.io | sh -s -- --prefix "$HOME/.local"
			export PATH="$HOME/.local/bin:$PATH"
		}
		command -v podman >/dev/null 2>&1 || die "podman installation failed — install it manually: https://podman.io/docs/installation"
		HAVE_PODMAN=1
		PODMAN_VER=$(podman version --format '{{.Client.Version}}' 2>/dev/null || echo "0")
		ui_ok "podman ${PODMAN_VER} installed"
	fi

	ui_ok "runtime: $RUNTIME"
}

ask_common() {
	ui_phase "4/6" "Settings"
	if [ -n "$ARG_PORT" ]; then
		PORT="$ARG_PORT"
	elif asked; then
		PORT=$(ui_input "Host port (published on 127.0.0.1 only)" "8585")
	else
		PORT="8585"
	fi
	[[ "$PORT" =~ ^[0-9]+$ ]] && [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] ||
		die "invalid port: $PORT"

	if [ "$RUNTIME" = "docker" ]; then
		local def_dir="$HOME/.local/share/liseur-sync"
		[ "$(id -u)" -eq 0 ] && def_dir="/opt/liseur-sync"
		if [ -n "$ARG_DIR" ]; then
			INSTALL_DIR="$ARG_DIR"
		elif asked; then
			INSTALL_DIR=$(ui_input "Install directory (compose.yaml + .env live here)" "$def_dir")
		else
			INSTALL_DIR="$def_dir"
		fi
		ui_ok "install dir: $INSTALL_DIR"
	fi
	ui_ok "url: http://127.0.0.1:${PORT}"
}

# COMPOSE_CMD: resolved once (array) so ui_spin/gum can exec it directly
# — gum spin runs its arguments as a real command, not a shell function.
COMPOSE_CMD=()

resolve_compose() {
	if docker compose version >/dev/null 2>&1; then
		COMPOSE_CMD=(docker compose)
	elif command -v docker-compose >/dev/null 2>&1; then
		COMPOSE_CMD=(docker-compose)
	else
		die "docker compose (plugin or standalone) not found"
	fi
}

compose() {
	[ ${#COMPOSE_CMD[@]} -gt 0 ] || resolve_compose
	"${COMPOSE_CMD[@]}" "$@"
}

wait_healthz() {
	local url="http://127.0.0.1:${PORT}/healthz" i
	ui_info "waiting for ${url} …"
	for i in $(seq 1 60); do
		if curl -fsS "$url" >/dev/null 2>&1; then
			ui_ok "server is healthy"
			return 0
		fi
		sleep 1
	done
	ui_err "server did not become healthy within 60s."
	return 1
}

install_docker() {
	ui_phase "5/6" "Install (Docker Compose)"
	mkdir -p "$INSTALL_DIR"
	local compose_file="$INSTALL_DIR/compose.yaml"

	if [ -f "$compose_file" ] &&
		{ ! grep -q 'LISEUR_CACHE_DIR:' "$compose_file" ||
			{ [ "$DB" = "postgres" ] &&
				{ ! grep -q 'content-data:/data' "$compose_file" ||
					! grep -q 'content-init:' "$compose_file"; }; }; }; then
		if ! asked; then
			die "existing compose.yaml predates persistent content storage; replace it with the ${GIT_REF} version before upgrading"
		fi
		local legacy_choice
		legacy_choice=$(ui_choose \
			"compose.yaml must be refreshed for persistent content storage" 1 \
			"Fetch a fresh copy from git ref '${GIT_REF}'" \
			"Abort")
		case "$legacy_choice" in
		1) rm -f "$compose_file" ;;
		2) die "aborted by user" ;;
		esac
	elif [ -f "$compose_file" ] && asked; then
		local c
		c=$(ui_choose "compose.yaml already exists in $INSTALL_DIR" 1 \
			"Keep the existing file" \
			"Fetch a fresh copy from git ref '${GIT_REF}'" \
			"Abort")
		case "$c" in
		2) rm -f "$compose_file" ;;
		3) die "aborted by user" ;;
		esac
	fi

	if [ ! -f "$compose_file" ]; then
		local tmp
		tmp=$(mktemp)
		curl -fsSL "$COMPOSE_URL" -o "$tmp" || die "failed to fetch $COMPOSE_URL"
		# The install dir has no build context; the published image is
		# always used, so drop the `build:` line from the anchor. Publish
		# on the requested port instead of the file's default.
		sed -e '/^  build: \.$/d' \
			-e "s|\"127.0.0.1:8585:8585\"|\"127.0.0.1:${PORT}:8585\"|" \
			"$tmp" >"$compose_file"
		rm -f "$tmp"
		ui_ok "fetched compose.yaml ($GIT_REF)"
	fi

	# .env with secrets, mode 600
	local env_file="$INSTALL_DIR/.env"
	if [ ! -f "$env_file" ]; then
		{
			echo "# generated by install.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ)"
			if [ "$DB" = "postgres" ]; then
				# hex-only: interpolated verbatim into the DSN by compose
				echo "POSTGRES_PASSWORD=$(rand_hex)"
			else
				# set anyway: compose.yaml references it in every profile
				echo "POSTGRES_PASSWORD="
			fi
		} >"$env_file"
		chmod 600 "$env_file"
		ui_ok "wrote .env"
	elif [ "$DB" = "postgres" ] && ! grep -q '^POSTGRES_PASSWORD=.' "$env_file"; then
		echo "POSTGRES_PASSWORD=$(rand_hex)" >>"$env_file"
		chmod 600 "$env_file"
		ui_ok "added POSTGRES_PASSWORD to existing .env"
	else
		ui_dim "keeping existing .env"
	fi

	compose version >/dev/null # resolves COMPOSE_CMD
	(cd "$INSTALL_DIR" && ui_spin "pulling images (this can take a minute)" \
		"${COMPOSE_CMD[@]}" --profile "$DB" pull) ||
		ui_warn "image pull failed; compose will try again on 'up'"

	(cd "$INSTALL_DIR" &&
		ui_spin "starting liseur-sync ($DB profile)" \
			"${COMPOSE_CMD[@]}" --profile "$DB" up -d) ||
		die "docker compose up failed"

	if ! wait_healthz; then
		(cd "$INSTALL_DIR" && compose --profile "$DB" logs --tail 40) || true
		die "check the logs above"
	fi
}

install_podman() {
	ui_phase "5/6" "Install (Podman + systemd)"

	if ! version_ge "$PODMAN_VER" "4.4"; then
		die "podman >= 4.4 is required for quadlets (found ${PODMAN_VER}). Upgrade podman: https://podman.io/docs/installation"
	fi
	if ! systemctl --user show-environment >/dev/null 2>&1; then
		die "no systemd user session available. Log in via a session with a user bus (or use --runtime=docker)."
	fi

	local image="${IMAGE_REPO}:${VERSION}"
	ui_spin "pulling ${image}" podman pull "$image" || die "image pull failed"

	podman volume exists liseur-sync-data 2>/dev/null || podman volume create liseur-sync-data >/dev/null

	local db_url="" pg_pw=""
	if [ "$DB" = "postgres" ]; then
		pg_pw=$(rand_hex)
		ui_spin "pulling postgres:17-alpine" podman pull docker.io/library/postgres:17-alpine
		podman volume exists liseur-sync-pgdata 2>/dev/null || podman volume create liseur-sync-pgdata >/dev/null
		db_url="postgres://liseur:${pg_pw}@liseur-sync-postgres:5432/liseur_sync"
	else
		db_url="/data/liseur-sync.db"
	fi

	# Quadlet files. The app data mount uses Podman's :U option so the
	# named volume is owned by the image's non-root UID inside the user
	# namespace; :Z applies the SELinux label.
	local qdir="$HOME/.config/containers/systemd"
	mkdir -p "$qdir"

	if [ "$DB" = "postgres" ]; then
		cat >"$qdir/liseur-sync.network" <<EOF
[Network]
NetworkName=liseur-sync
EOF
		cat >"$qdir/liseur-sync-postgres.container" <<EOF
[Unit]
Description=liseur-sync bundled PostgreSQL

[Container]
Image=docker.io/library/postgres:17-alpine
ContainerName=liseur-sync-postgres
Network=liseur-sync.network
Volume=liseur-sync-pgdata:/var/lib/postgresql/data:Z
Environment=POSTGRES_USER=liseur
Environment=POSTGRES_PASSWORD=${pg_pw}
Environment=POSTGRES_DB=liseur_sync
HealthCmd=pg_isready -U liseur -d liseur_sync
HealthInterval=5s
HealthRetries=12

[Service]
Restart=always

[Install]
WantedBy=default.target
EOF
	fi

	{
		echo "[Unit]"
		echo "Description=liseur-sync reading sync server"
		echo "After=network-online.target"
		if [ "$DB" = "postgres" ]; then
			# Requires + After + restart-on-failure: the app exits if the
			# DSN is unreachable (no in-app retry), so ordering plus the
			# restart loop converges once postgres is accepting.
			echo "Requires=liseur-sync-postgres.service"
			echo "After=liseur-sync-postgres.service"
		fi
		echo ""
		echo "[Container]"
		echo "Image=${image}"
		echo "ContainerName=liseur-sync"
		[ "$DB" = "postgres" ] && echo "Network=liseur-sync.network"
		cat <<EOF
PublishPort=127.0.0.1:${PORT}:8585
Volume=liseur-sync-data:/data:Z,U
Environment=LISEUR_LISTEN_ADDR=0.0.0.0:8585
Environment=LISEUR_DATABASE_DRIVER=${DB}
Environment=LISEUR_DATABASE_URL=${db_url}
Environment=LISEUR_CACHE_DIR=/data/cache
Exec=serve

[Service]
Restart=always

[Install]
WantedBy=default.target
EOF
	} >"$qdir/liseur-sync.container"

	if [ "$DB" = "postgres" ]; then
		chmod 600 "$qdir/liseur-sync-postgres.container" "$qdir/liseur-sync.container"
		ui_ok "wrote quadlets: liseur-sync.{container,network} + liseur-sync-postgres.container"
	else
		ui_ok "wrote quadlet: $qdir/liseur-sync.container"
	fi

	systemctl --user daemon-reload
	if [ "$DB" = "postgres" ]; then
		ui_spin "starting liseur-sync + postgres" systemctl --user start liseur-sync-postgres liseur-sync
	else
		ui_spin "starting liseur-sync" systemctl --user start liseur-sync
	fi || {
		journalctl --user -u liseur-sync --no-pager -n 40 || true
		die "service failed to start (logs above)"
	}

	if loginctl show-user "$USER" --property=Linger 2>/dev/null | grep -q 'Linger=no'; then
		ui_info "enabling lingering so the service survives logout…"
		if ! loginctl enable-linger "$USER" 2>/dev/null; then
			$SUDO loginctl enable-linger "$USER" ||
				ui_warn "could not enable lingering; the service stops when you log out. Run: sudo loginctl enable-linger $USER"
		fi
	fi

	wait_healthz || {
		journalctl --user -u liseur-sync --no-pager -n 40 || true
		die "check the logs above"
	}
}

# ---------------------------------------------------------------- bootstrap

# app_container -> prints the running app container id (docker path)
app_container() {
	local svc="app-sqlite"
	[ "$DB" = "postgres" ] && svc="app-pg"
	(cd "$INSTALL_DIR" && compose --profile "$DB" ps -q "$svc" 2>/dev/null) | head -n1
}

run_admin() {
	if [ "$RUNTIME" = "docker" ]; then
		# docker exec -i keeps stdin open; compose's non-TTY exec closes
		# it, which would break the binary's password prompt.
		local cid
		cid=$(app_container)
		[ -n "$cid" ] || die "app container not found (is the stack up?)"
		docker exec -i "$cid" "$APP_BIN" admin "$@"
	else
		podman exec -i liseur-sync "$APP_BIN" admin "$@"
	fi
}

bootstrap() {
	ui_phase "6/6" "First user"
	asked || {
		ui_dim "non-interactive mode — skipping bootstrap. Create a user with the admin command from the summary."
		return 0
	}

	if ! ui_confirm "Create the first user account now?" 1; then
		ui_dim "skipped — create one later with the admin command from the summary."
		return 0
	fi
	local name
	name=$(ui_input "Username" "${USER:-admin}")

	# The admin binary prompts for the password on the TTY itself; with
	# no controlling terminal it reads piped stdin instead. Redirect from
	# the tty when one exists so the prompt is interactive even when the
	# script's stdin was a pipe.
	local have_tty=0
	if (exec 3<>/dev/tty) 2>/dev/null; then
		exec 3>&-
		have_tty=1
		tty_restore # gum TUI may have left the terminal in raw mode
	fi

	ui_info "the server will prompt for a password (min 8 chars)."
	local created=0
	if [ "$have_tty" = 1 ]; then
		run_admin create-user "$name" </dev/tty && created=1
	else
		run_admin create-user "$name" && created=1
	fi
	if [ "$created" = 0 ]; then
		ui_warn "create-user failed — skipping token/pairing steps. Retry later with the admin command from the summary."
		return 0
	fi

	if ui_confirm "Mint a device token for '${name}' (e.g. for the Liseur app)?" 1; then
		local dev
		dev=$(ui_input "Device name" "my-reader")
		ui_info "the secret below is shown exactly once — save it now:"
		run_admin mint-token "$name" "$dev" || ui_warn "mint-token failed"
	fi

	if ui_confirm "Generate a KOReader kosync pairing code (15-minute TTL)?" 1; then
		run_admin pairing-code "$name" || ui_warn "pairing-code failed"
	fi
}

# ---------------------------------------------------------------- summary

summary() {
	local manage admin_cmd db_desc
	if [ "$RUNTIME" = "docker" ]; then
		manage="cd ${INSTALL_DIR} && docker compose --profile ${DB} {ps,logs -f,down}"
		admin_cmd="cd ${INSTALL_DIR} && docker compose --profile ${DB} exec app-$([ "$DB" = postgres ] && echo pg || echo sqlite) ${APP_BIN} admin …"
		db_desc="${DB} (docker volume)"
	else
		manage="systemctl --user {status,restart} liseur-sync · journalctl --user -u liseur-sync -f"
		admin_cmd="podman exec -it liseur-sync ${APP_BIN} admin …"
		db_desc="${DB} (podman volume liseur-sync-data)"
	fi

	ui_summary <<EOF
${_c_bold}liseur-sync is up${_c_reset}

  URL       http://127.0.0.1:${PORT}
  Database  ${db_desc}
  Runtime   ${RUNTIME}

  Manage    ${manage}
  Admin     ${admin_cmd}

  Next steps
  · Put a TLS reverse proxy in front (Caddy/nginx) — see docs/deployment.md.
    The server binds 127.0.0.1 only; without a trusted proxy it refuses
    credential traffic over plain HTTP.
  · LAN-only without TLS: set LISEUR_INSECURE_HTTP=true (see docs).
  · Backups: SQLite → VACUUM INTO, Postgres → pg_dump (docs/deployment.md).
  · Pair KOReader: admin pairing-code <user>  (15-minute TTL, single use)
EOF
	ui_ok "done — happy reading!"
}

# ---------------------------------------------------------------- main

main() {
	preflight
	choose_db
	choose_runtime
	ask_common
	case "$RUNTIME" in
	docker) install_docker ;;
	podman) install_podman ;;
	esac
	bootstrap
	summary
}

main "$@"
