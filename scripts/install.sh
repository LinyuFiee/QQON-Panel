#!/bin/bash
# QQON-Panel interactive installer protocol: 4
# Review this local file before running it as root. No remote script is executed.
set -euo pipefail
if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then
    printf '%s\n' '请直接执行 install.sh，不要使用 source 加载。' >&2
    return 1
fi
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
export LANG=C LC_ALL=C
unset BASH_ENV ENV PYTHONPATH PYTHONHOME SSL_CERT_FILE SSL_CERT_DIR APT_CONFIG

bootstrap_fail() {
    local message=$*
    case $message in
        'Use a valid public GitHub OWNER/REPO, not a URL.') message='请使用合法的公开 GitHub OWNER/REPO，而不是 URL。' ;;
        'Only stable vMAJOR.MINOR.PATCH releases are supported.') message='仅支持 v主版本.次版本.修订号 格式的稳定版本。' ;;
        'Version component is too large.') message='版本号分量超过允许上限。' ;;
        'Origin must be an HTTPS domain without credentials, path, query or fragment.') message='代理地址必须是 HTTPS 域名，不得包含凭据、路径、查询或片段。' ;;
        'Origin requires a domain name, not an IP address or localhost.') message='代理地址需要域名，不能使用 IP 或 localhost。' ;;
        'Invalid origin domain.') message='代理地址的域名格式无效。' ;;
        'Invalid origin port.') message='代理地址中的端口无效。' ;;
        'Port must be an integer from 1024 to 65535.') message='端口必须是 1024–65535 的整数。' ;;
        'No controlling terminal;'*) message='没有控制终端；请使用明确的 --yes / --upgrade / --status 参数，不能从管道读取确认。' ;;
        'Missing value for '*) message='命令行参数缺少值，请查看 --help。' ;;
        'Unknown command-line option:'*) message='不支持该命令行参数，请查看 --help。' ;;
        'Duplicate command-line option.') message='命令行参数重复。' ;;
        'Choose exactly one management action.') message='一次只能选择一个管理操作。' ;;
        'Update/status settings come only from root-owned install.json;'*) message='升级与状态参数只能读取 root 所有的 install.json，不能覆盖仓库、地址或端口。' ;;
        '--status accepts no mutation options.') message='--status 是只读操作，不接受变更参数。' ;;
        'Management requires root;'*) message='变更操作需要 root；未更改软件包或服务。' ;;
        'Duplicate operating-system identity field.') message='系统身份文件包含重复字段。' ;;
        'Invalid operating-system version.') message='系统版本格式无效。' ;;
        'Supported systems:'*) message='仅支持 Debian 12/13、Ubuntu 22.04/24.04、Rocky/AlmaLinux 9/10。' ;;
        'Management requires Linux.') message='此管理器需要 Linux 系统。' ;;
        'Cannot read operating-system identity.') message='无法读取系统身份文件。' ;;
        'Running systemd is required.'|'Running systemd is required;'*) message='需要运行中的 systemd；不会向容器安装 systemd。' ;;
        'The supported base system must provide uname.') message='系统缺少基础 uname 工具。' ;;
        'Only Linux amd64/arm64 are supported.') message='仅支持 Linux amd64/arm64。' ;;
        'Update mode requires the fixed root-owned installed script and recognized installation metadata.') message='升级必须运行固定 root 管理脚本，并具有受支持的安装记录。' ;;
        'Cancelled;'*) message='已取消；依赖、应用及服务均未更改。' ;;
        'No dependencies were selected.') message='没有选择需要安装的依赖。' ;;
        'Unapproved dependency package.') message='该依赖不在允许列表内。' ;;
        'The supported base system must provide apt-get and coreutils (env/timeout).') message='系统需提供 apt-get 和 coreutils（env/timeout）。' ;;
        'The supported base system must provide dnf and coreutils (env/timeout).') message='系统需提供 dnf 和 coreutils（env/timeout）。' ;;
        'APT index update failed;'*) message='APT 索引更新失败；未触碰已有应用和服务。' ;;
        'APT dependency installation failed;'*) message='APT 依赖安装失败；未触碰已有应用和服务。' ;;
        'DNF dependency installation failed;'*) message='DNF 依赖安装失败；未触碰已有应用和服务。' ;;
        'Dependency remains unavailable after package installation:'*) message='依赖安装后仍不可用；未触碰应用和服务，请检查系统包管理器。' ;;
        'Dependency recheck failed:'*) message='依赖复检未通过；未触碰应用和服务。' ;;
        'Unknown package manager.') message='系统包管理器不受支持。' ;;
    esac
    printf 'QQON 安装器：%s\n' "$message" >&2
    exit 1
}

bootstrap_help() {
    printf '%s\n' \
        '用法：sudo bash install.sh                         # 中文交互安装向导' \
        '      sudo bash install.sh [--repo OWNER/REPO] [--version v0.6.0] [--mode MODE] [--port PORT] [--host HOST] [--yes]' \
        '      sudo bash install.sh --mode proxy --origin https://panel.example.com' \
        '      sudo bash install.sh --mode public-certificate --host HOST --cert /绝对路径/cert.pem --key /绝对路径/key.pem' \
        '      sudo bash install.sh --upgrade [--version v0.6.0] [--yes]' \
        '      bash install.sh --status                    # 只读，不安装依赖' \
        '      qqon [status|url|start|stop|restart|logs|update|backup|config]' \
        '      qqon tls renew                              # 仅自签证书续期' \
        '' \
        '默认仓库 LinyuFiee/QQON-Panel；安装、升级仍要求 format 3 官方发行结构。' \
        '默认模式 public-selfsigned，IPv4 公网直连 HTTPS 8443；必须确认主机地址，不查询外部 IP。' \
        '其他模式 local（仅本机）、proxy（已有 HTTPS 代理）、public-certificate（导入证书）。' \
        '端口 1024–65535；不会自动配置代理、信任根证书或修改防火墙。' \
        '支持 Debian 12/13、Ubuntu 22.04/24.04、Rocky/AlmaLinux 9/10，Linux amd64/arm64 与运行中的 systemd。' \
        '只安装缺少的 Python 3.8+、CA 和账户工具，不升级系统、不改变 SELinux。' \
        'v0.4/v0.5 已知管理器迁移必须单独在终端确认，--yes 不跳过；旧账户与数据保留。' \
        '--help、-h：显示帮助，不更改主机。'
}

bootstrap_repo() {
    [[ $1 =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,98}[A-Za-z0-9])?/[A-Za-z0-9]([A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?$ ]] ||
        bootstrap_fail 'Use a valid public GitHub OWNER/REPO, not a URL.'
}

bootstrap_version() {
    [[ $1 =~ ^v(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})$ ]] ||
        bootstrap_fail 'Only stable vMAJOR.MINOR.PATCH releases are supported.'
    (( 10#${BASH_REMATCH[1]} <= 2147483647 && 10#${BASH_REMATCH[2]} <= 2147483647 && 10#${BASH_REMATCH[3]} <= 2147483647 )) ||
        bootstrap_fail 'Version component is too large.'
}

bootstrap_origin() {
    [[ -n $1 ]] || return 0
    [[ ${#1} -le 300 && $1 =~ ^https://([A-Za-z0-9.-]+)(:([0-9]+))?/?$ ]] ||
        bootstrap_fail 'Origin must be an HTTPS domain without credentials, path, query or fragment.'
    local host=${BASH_REMATCH[1]} port=${BASH_REMATCH[3]-} label rest
    [[ ${#host} -le 253 && $host == *.* && $host != *. && ! $host =~ ^[0-9.]+$ ]] ||
        bootstrap_fail 'Origin requires a domain name, not an IP address or localhost.'
    rest=$host
    while :; do
        label=${rest%%.*}
        [[ $label =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$ ]] || bootstrap_fail 'Invalid origin domain.'
        [[ $rest == *.* ]] || break
        rest=${rest#*.}
    done
    if [[ -n $port ]]; then
        while [[ ${#port} -gt 1 && $port == 0* ]]; do port=${port#0}; done
        [[ ${#port} -le 5 ]] && (( 10#$port >= 1 && 10#$port <= 65535 )) || bootstrap_fail 'Invalid origin port.'
    fi
}

bootstrap_port() {
    [[ $1 =~ ^[1-9][0-9]{3,4}$ ]] && (( 10#$1 >= 1024 && 10#$1 <= 65535 )) ||
        bootstrap_fail 'Port must be an integer from 1024 to 65535.'
}

bootstrap_prompt() {
    { printf '%s' "$1" > /dev/tty && IFS= read -r REPLY < /dev/tty; } 2>/dev/null ||
        bootstrap_fail 'No controlling terminal; use explicit --yes/--upgrade/--status flags instead of the interactive menu.'
}

bootstrap_menu() {
    local REPLY='' port mode
    while :; do
        bootstrap_prompt $'QQON-Panel（官方仓库 LinyuFiee/QQON-Panel）\n1 安装\n2 升级 / 迁移已知旧管理器\n3 查看状态（只读）\n0 退出\n请选择 [0]：'
        case $REPLY in
            0|'') exit 0 ;;
            3) PANEL_ARGS=(--status); return ;;
            2) PANEL_ARGS=(--upgrade); return ;;
            1) break ;;
            *) printf '%s\n' '请输入 1、2、3 或 0。' ;;
        esac
    done
    while :; do
        bootstrap_prompt $'1 公网 IPv4 自签 HTTPS（默认，需浏览器核对指纹）\n2 仅本机 / SSH 隧道\n3 已有 HTTPS 反向代理\n4 公网 HTTPS 导入证书\n访问方式 [1]：'
        case ${REPLY:-1} in
            1) mode=public-selfsigned; port=8443; break ;;
            2) mode=local; port=5173; break ;;
            3) mode=proxy; port=5173; break ;;
            4) mode=public-certificate; port=8443; break ;;
            *) printf '%s\n' '请输入 1、2、3 或 4。' ;;
        esac
    done
    PANEL_ARGS=(--mode "$mode")
    while :; do
        bootstrap_prompt "监听端口，1024–65535 [$port]："
        if (bootstrap_port "${REPLY:-$port}"); then port=${REPLY:-$port}; break; fi
    done
    PANEL_ARGS+=(--port "$port")
    case $mode in
        proxy)
            while :; do
                bootstrap_prompt '已有代理的 HTTPS 地址（例如 https://panel.example.com）：'
                if [[ -n $REPLY ]] && (bootstrap_origin "$REPLY"); then PANEL_ARGS+=(--origin "$REPLY"); break; fi
            done ;;
        public-*)
            bootstrap_prompt '请确认并输入本服务器的公网 IPv4（导入证书也可用域名；不会查询外部 IP）：'
            PANEL_ARGS+=(--host "$REPLY")
            if [[ $mode == public-certificate ]]; then
                bootstrap_prompt '证书 PEM 文件绝对路径：'; PANEL_ARGS+=(--cert "$REPLY")
                bootstrap_prompt '私钥 PEM 文件绝对路径：'; PANEL_ARGS+=(--key "$REPLY")
            fi ;;
    esac
}

bootstrap_manage_arguments() {
    PANEL_BOOTSTRAP_MANAGE=1
    local action=${1:-menu} option value seen=' '
    (( $# == 0 )) || shift
    case $action in
        menu|status|url|start|stop|restart|logs|update|backup|config) ;;
        tls) [[ ${1-} == renew ]] || bootstrap_fail '证书维护仅支持 qqon tls renew。'; shift; action=renew ;;
        *) bootstrap_fail '不支持该维护命令；请运行 qqon 查看菜单。' ;;
    esac
    case $action in menu|status|url|logs) PANEL_BOOTSTRAP_STATUS=1 ;; esac
    while (( $# )); do
        option=$1; shift
        case $option in
            --yes) value='' ;;
            --version|--lines|--mode|--port|--origin|--host|--cert|--key)
                (( $# )) || bootstrap_fail '维护参数缺少值。'; value=$1; shift ;;
            --version=*|--lines=*|--mode=*|--port=*|--origin=*|--host=*|--cert=*|--key=*) value=${option#*=}; option=${option%%=*} ;;
            *) bootstrap_fail '不支持该维护参数；服务与管理路径不可覆盖。' ;;
        esac
        [[ $seen != *" $option "* ]] || bootstrap_fail '命令行参数重复。'
        seen+="$option "
        case $action:$option in
            start:--yes|stop:--yes|restart:--yes|update:--yes|backup:--yes|config:--yes|renew:--yes) PANEL_BOOTSTRAP_YES=1 ;;
            update:--version) bootstrap_version "$value" ;;
            logs:--lines) [[ $value =~ ^[1-9][0-9]{0,2}$ ]] && (( 10#$value <= 200 )) || bootstrap_fail '日志行数范围为 1–200。' ;;
            config:--mode) [[ $value == local || $value == proxy || $value == public-selfsigned || $value == public-certificate ]] || bootstrap_fail '访问方式不受支持。' ;;
            config:--port) bootstrap_port "$value" ;;
            config:--origin) bootstrap_origin "$value" ;;
            config:--host) [[ $value =~ ^[A-Za-z0-9.-]{1,253}$ ]] || bootstrap_fail '主机必须是 IPv4 或 ASCII 域名。' ;;
            config:--cert|config:--key) [[ $value == /* && $value != *$'\n'* && $value != *$'\r'* && ${#value} -le 4096 ]] || bootstrap_fail '证书来源必须是合法绝对路径。' ;;
            *) bootstrap_fail '该维护命令不接受这些参数；只读命令不能带变更选项。' ;;
        esac
    done
}

bootstrap_arguments() {
    PANEL_BOOTSTRAP_YES=0
    PANEL_BOOTSTRAP_UPDATE=0
    PANEL_BOOTSTRAP_STATUS=0
    PANEL_BOOTSTRAP_MANAGE=0
    if [[ ${1-} == --manage ]]; then shift; bootstrap_manage_arguments "$@"; return; fi
    local option name value seen=' ' has_settings=0 actions=0 has_version=0
    while (( $# )); do
        option=$1
        shift
        case $option in
            --help|-h) bootstrap_help; exit 0 ;;
            --yes|--update|--upgrade|--status|--migrate-manager) name=$option; value='' ;;
            --repo|--version|--origin|--port|--mode|--host|--cert|--key)
                name=$option
                (( $# )) || bootstrap_fail "Missing value for $name."
                value=$1
                shift ;;
            --repo=*|--version=*|--origin=*|--port=*|--mode=*|--host=*|--cert=*|--key=*) name=${option%%=*}; value=${option#*=} ;;
            *) bootstrap_fail "Unknown command-line option: $option" ;;
        esac
        [[ $seen != *" $name "* ]] || bootstrap_fail 'Duplicate command-line option.'
        seen+="$name "
        case $name in
            --yes) PANEL_BOOTSTRAP_YES=1 ;;
            --update) PANEL_BOOTSTRAP_UPDATE=1; actions=$((actions + 1)) ;;
            --upgrade|--migrate-manager) actions=$((actions + 1)) ;;
            --status) PANEL_BOOTSTRAP_STATUS=1; actions=$((actions + 1)) ;;
            --repo) bootstrap_repo "$value"; has_settings=1 ;;
            --version) bootstrap_version "$value"; has_version=1 ;;
            --origin) bootstrap_origin "$value"; has_settings=1 ;;
            --port) bootstrap_port "$value"; has_settings=1 ;;
            --mode) [[ $value == local || $value == proxy || $value == public-selfsigned || $value == public-certificate ]] || bootstrap_fail '访问方式不受支持。'; has_settings=1 ;;
            --host) [[ $value =~ ^[A-Za-z0-9.-]{1,253}$ ]] || bootstrap_fail '主机必须是 IPv4 或 ASCII 域名。'; has_settings=1 ;;
            --cert|--key) [[ $value == /* && $value != *$'\n'* && $value != *$'\r'* && ${#value} -le 4096 ]] || bootstrap_fail '证书来源必须是合法绝对路径。'; has_settings=1 ;;
        esac
    done
    (( actions <= 1 )) || bootstrap_fail 'Choose exactly one management action.'
    if (( actions )); then
        (( !has_settings )) || bootstrap_fail 'Update/status settings come only from root-owned install.json; --repo/--origin/--port are forbidden.'
    fi
    (( !PANEL_BOOTSTRAP_STATUS || (!has_version && !PANEL_BOOTSTRAP_YES) )) || bootstrap_fail '--status accepts no mutation options.'
}

bootstrap_root() {
    (( EUID == 0 )) || bootstrap_fail 'Management requires root; no packages or services were changed.'
}

bootstrap_os_release() {
    # os-release is DATA, never shell code. Only literal ID and VERSION_ID values
    # matter; shell escapes, expansions and duplicate fields are not accepted.
    local line key value id='' version='' seen=' '
    while IFS= read -r line || [[ -n $line ]]; do
        case $line in
            ID=*|VERSION_ID=*)
                key=${line%%=*}; value=${line#*=}
                [[ $seen != *" $key "* ]] || bootstrap_fail 'Duplicate operating-system identity field.'
                seen+="$key "
                case $value in
                    \"*\") value=${value:1:${#value}-2} ;;
                    \'*\') value=${value:1:${#value}-2} ;;
                esac
                if [[ $key == ID ]]; then id=$value; else version=$value; fi ;;
        esac
    done
    case $id:$version in
        debian:12|debian:13|ubuntu:22.04|ubuntu:24.04)
            PANEL_PACKAGE_MANAGER=apt
            PANEL_CA_FILE=/etc/ssl/certs/ca-certificates.crt
            PANEL_ACCOUNT_PACKAGE=passwd
            PANEL_NOLOGIN_PACKAGE=login ;;
        rocky:9|rocky:9.[0-9]*|rocky:10|rocky:10.[0-9]*|almalinux:9|almalinux:9.[0-9]*|almalinux:10|almalinux:10.[0-9]*)
            [[ $version =~ ^(9|10)(\.[0-9]+)?$ ]] || bootstrap_fail 'Invalid operating-system version.'
            PANEL_PACKAGE_MANAGER=dnf
            PANEL_CA_FILE=/etc/pki/tls/certs/ca-bundle.crt
            PANEL_ACCOUNT_PACKAGE=shadow-utils
            PANEL_NOLOGIN_PACKAGE=util-linux-core ;;
        *) bootstrap_fail 'Supported systems: Debian 12/13, Ubuntu 22.04/24.04, Rocky/AlmaLinux 9/10 only.' ;;
    esac
}

bootstrap_system() {
    local kernel='' init='' arch=''
    [[ -r /proc/sys/kernel/ostype ]] && IFS= read -r kernel < /proc/sys/kernel/ostype ||
        bootstrap_fail 'Management requires Linux.'
    [[ $kernel == Linux ]] || bootstrap_fail 'Management requires Linux.'
    [[ -r /etc/os-release ]] || bootstrap_fail 'Cannot read operating-system identity.'
    bootstrap_os_release < /etc/os-release
    [[ -r /proc/1/comm ]] && IFS= read -r init < /proc/1/comm || bootstrap_fail 'Running systemd is required.'
    [[ $init == systemd && -d /run/systemd/system && ! -L /run/systemd/system && -x /usr/bin/systemctl ]] ||
        bootstrap_fail 'Running systemd is required; systemd will not be installed into a container.'
    [[ -x /usr/bin/uname ]] || bootstrap_fail 'The supported base system must provide uname.'
    arch=$(/usr/bin/uname -m)
    case $arch in x86_64|aarch64) ;; *) bootstrap_fail 'Only Linux amd64/arm64 are supported.' ;; esac
}

bootstrap_update_source() {
    if (( PANEL_BOOTSTRAP_UPDATE )); then
        [[ ${BASH_SOURCE[0]} == /usr/local/lib/server-panel/install.sh && -f /etc/server-panel/install.json && ! -L /etc/server-panel/install.json ]] ||
            bootstrap_fail 'Update mode requires the fixed root-owned installed script and recognized installation metadata.'
    fi
}

bootstrap_python_ready() {
    [[ -x /usr/bin/python3 ]] && /usr/bin/python3 -I -c \
        'import sys, ssl, tarfile, urllib.request, zlib; sys.exit(0 if sys.version_info >= (3, 8) else 1)' >/dev/null 2>&1
}

bootstrap_ca_ready() {
    [[ -r $PANEL_CA_FILE && -s $PANEL_CA_FILE ]] || return 1
    # Recheck actual CA contents after installing Python; never use insecure TLS.
    (( $1 )) || return 0
    /usr/bin/python3 -I -c \
        'import ssl, sys; ctx = ssl.create_default_context(cafile=sys.argv[1]); sys.exit(0 if ctx.cert_store_stats()["x509_ca"] else 1)' "$PANEL_CA_FILE" >/dev/null 2>&1
}

bootstrap_account_tools_ready() {
    [[ -x /usr/sbin/useradd && -x /usr/sbin/groupadd ]]
}

bootstrap_nologin_ready() {
    [[ -x /usr/sbin/nologin ]]
}

bootstrap_missing() {
    PANEL_MISSING=()
    local python_ready=0
    if bootstrap_python_ready; then python_ready=1; else PANEL_MISSING+=(python3); fi
    bootstrap_ca_ready "$python_ready" || PANEL_MISSING+=(ca-certificates)
    bootstrap_account_tools_ready || PANEL_MISSING+=("$PANEL_ACCOUNT_PACKAGE")
    bootstrap_nologin_ready || PANEL_MISSING+=("$PANEL_NOLOGIN_PACKAGE")
}

bootstrap_answer() {
    # Separate write/read descriptors work with a non-seekable controlling TTY.
    { printf '%s' '输入 yes，仅安装下面列出的缺失依赖：' > /dev/tty &&
        IFS= read -r -n 16 REPLY < /dev/tty; } 2>/dev/null ||
        bootstrap_fail 'No controlling terminal; use --yes only for explicitly unattended operation.'
}

bootstrap_confirm() {
    printf '缺少或不可用的最小依赖：%s\n' "$*"
    printf '%s 仅安装缺失的允许依赖；不升级系统，不更换软件源或 SELinux 策略。\n' "$PANEL_PACKAGE_MANAGER"
    if (( !PANEL_BOOTSTRAP_YES )); then
        local REPLY=''
        bootstrap_answer
        [[ $REPLY == yes ]] || bootstrap_fail 'Cancelled; packages, panel binary and service were not changed.'
    fi
}

bootstrap_apt() {
    local package
    (( $# )) || bootstrap_fail 'No dependencies were selected.'
    for package in "$@"; do
        case $package in python3|ca-certificates|passwd|login) ;; *) bootstrap_fail 'Unapproved dependency package.' ;; esac
    done
    [[ -x /usr/bin/apt-get && -x /usr/bin/timeout && -x /usr/bin/env ]] ||
        bootstrap_fail 'The supported base system must provide apt-get and coreutils (env/timeout).'
    # Clean environment, fixed commands, bounded lock/retry/network and overall
    # deadlines. Never upgrade the system, rewrite sources or run downloaded code.
    /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/ LANG=C LC_ALL=C DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l \
        /usr/bin/timeout --signal=TERM --kill-after=30s 180s /usr/bin/apt-get \
        -o DPkg::Lock::Timeout=30 -o Acquire::Retries=2 -o Acquire::http::Timeout=15 -o Acquire::https::Timeout=15 \
        -o APT::Update::Error-Mode=any update ||
        bootstrap_fail 'APT index update failed; the existing panel binary and service were not touched.'
    /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/ LANG=C LC_ALL=C DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l \
        /usr/bin/timeout --signal=TERM --kill-after=30s 300s /usr/bin/apt-get \
        -o DPkg::Lock::Timeout=30 -o Acquire::Retries=2 -o Acquire::http::Timeout=15 -o Acquire::https::Timeout=15 \
        install --yes --no-install-recommends "$@" ||
        bootstrap_fail 'APT dependency installation failed; the existing panel binary and service were not touched.'
}

bootstrap_dnf() {
    local package
    (( $# )) || bootstrap_fail 'No dependencies were selected.'
    for package in "$@"; do
        case $package in python3|ca-certificates|shadow-utils|util-linux-core) ;; *) bootstrap_fail 'Unapproved dependency package.' ;; esac
    done
    [[ -x /usr/bin/dnf && -x /usr/bin/timeout && -x /usr/bin/env ]] ||
        bootstrap_fail 'The supported base system must provide dnf and coreutils (env/timeout).'
    /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/ LANG=C LC_ALL=C \
        /usr/bin/timeout --signal=TERM --kill-after=30s 300s /usr/bin/dnf \
        --setopt=timeout=15 --setopt=retries=2 --setopt=exit_on_lock=True --setopt=install_weak_deps=False \
        install --assumeyes "$@" ||
        bootstrap_fail 'DNF dependency installation failed; the existing panel binary and service were not touched.'
}

bootstrap() {
    PANEL_ARGS=("$@")
    (( ${#PANEL_ARGS[@]} )) || bootstrap_menu
    bootstrap_arguments "${PANEL_ARGS[@]}"
    if (( PANEL_BOOTSTRAP_STATUS )); then
        bootstrap_python_ready || bootstrap_fail '只读查询需要已有 Python 3.8+；未安装任何依赖。'
        return 0
    fi
    bootstrap_root
    bootstrap_system
    bootstrap_update_source
    if (( PANEL_BOOTSTRAP_MANAGE )); then
        bootstrap_python_ready || bootstrap_fail '维护命令需要已有 Python 3.8+；请使用经审阅的安装器修复依赖。'
        return 0
    fi
    local attempted=' ' package round
    # Two bounded passes: installing Python may expose a malformed CA store.
    for round in 1 2; do
        bootstrap_missing
        (( ${#PANEL_MISSING[@]} )) || return 0
        for package in "${PANEL_MISSING[@]}"; do
            [[ $attempted != *" $package "* ]] || bootstrap_fail "Dependency remains unavailable after package installation: $package. Panel service/binary untouched."
        done
        bootstrap_confirm "${PANEL_MISSING[@]}"
        for package in "${PANEL_MISSING[@]}"; do attempted+="$package "; done
        case $PANEL_PACKAGE_MANAGER in
            apt) bootstrap_apt "${PANEL_MISSING[@]}" ;;
            dnf) bootstrap_dnf "${PANEL_MISSING[@]}" ;;
            *) bootstrap_fail 'Unknown package manager.' ;;
        esac
    done
    bootstrap_missing
    (( ${#PANEL_MISSING[@]} == 0 )) || bootstrap_fail "Dependency recheck failed: ${PANEL_MISSING[*]}. Panel service/binary untouched."
}

# Tests compile the definitions above in an isolated harness; there is no runtime
# environment/CLI switch for bypassing bootstrap or choosing production paths.
bootstrap "$@"
set -- "${PANEL_ARGS[@]}"
# The source descriptor comes from Bash, never from a user-supplied CLI option.
exec 4<"${BASH_SOURCE[0]}"
exec /usr/bin/python3 -I -X utf8 - "${BASH_SOURCE[0]}" "$@" <<'PANEL_PYTHON'
import argparse
import base64
import contextlib
import datetime
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
import selectors
import secrets
import signal
import socket
import ssl
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zlib

BINARY_LIMIT = 64 * 1024 * 1024
ARCHIVE_LIMIT = 64 * 1024 * 1024
TAR_LIMIT = BINARY_LIMIT + 10240
MANIFEST_LIMIT = 32 * 1024
SUMS_LIMIT = 16 * 1024
API_LIMIT = 1024 * 1024
SCRIPT_LIMIT = 512 * 1024
WRAPPER_LIMIT = 32 * 1024
UNIT = "server-panel.service"
ACCOUNT = "server-panel"
DEFAULT_REPO = "LinyuFiee/QQON-Panel"
LISTEN = "127.0.0.1:5173"
LEGACY_INSTALLER_SHA256 = "bc78f2ec682704bb5beb965806ec56e511827f6d3451949a03d5b40db535eb91"
LEGACY_WRAPPER_SHA256 = "5cdc4f05cce7189b7212e5b12496e0754985c45c1462fc2ce0aa1a86f81b1408"
V05_INSTALLER_SHA256 = "9af6eaf93bbdd5f622bc9bc7b37e4a6f1f218298e7a403ee2d7cfffbd8630936"
CERT_LIMIT = 256 * 1024
BACKUP_LIMIT = 512 * 1024 * 1024
BACKUP_FILES = 10000
LOG_LIMIT = 128 * 1024
ACCESS_MODES = ("local", "proxy", "public-selfsigned", "public-certificate")
ACCESS_FIELDS = ("listen", "origin", "healthHost", "accessMode", "tls")
LEGACY_NAMES = {"panel-linux-amd64.tar.gz", "panel-linux-arm64.tar.gz", "install.sh", "update.sh", "release.json", "SHA256SUMS"}
NAMES = LEGACY_NAMES | {"panel-windows-amd64.zip", "install.ps1", "update.ps1"}
BINARY_TARGETS = {("linux", "amd64"): ("panel", "panel-linux-amd64.tar.gz"),
                  ("linux", "arm64"): ("panel", "panel-linux-arm64.tar.gz"),
                  ("windows", "amd64"): ("panel.exe", "panel-windows-amd64.zip")}
CDN_HOSTS = {"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com"}
HEX = re.compile(r"[0-9a-f]{64}\Z", re.ASCII)
COMMIT = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z", re.ASCII)
VERSION = re.compile(r"(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\Z", re.ASCII)
WRAPPER = b'''#!/bin/bash -p
# Fixed, root-managed entry point; all arguments are validated by install.sh.
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
unset BASH_ENV ENV PYTHONPATH PYTHONHOME
exec /bin/bash -p /usr/local/lib/server-panel/install.sh --update "$@"
'''
QQON_WRAPPER = b'''#!/bin/bash -p
# QQON fixed management entry; install.sh validates every argument.
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
unset BASH_ENV ENV PYTHONPATH PYTHONHOME
exec /bin/bash -p /usr/local/lib/server-panel/install.sh --manage "$@"
'''


def legacy_hashes():
    return (LEGACY_INSTALLER_SHA256, V05_INSTALLER_SHA256)


class Failure(Exception):
    pass


class Interrupted(BaseException):
    # Do not let health/network retry handlers swallow an administrator's signal.
    pass


def require(condition, message):
    if not condition:
        raise Failure(message)


def integer(value, low, high):
    return type(value) is int and low <= value <= high


def version_tuple(value, tagged=False):
    require(isinstance(value, str), "Version must be a stable semantic version.")
    if tagged:
        require(value.startswith("v"), "Release versions must have the v prefix.")
        value = value[1:]
    match = VERSION.fullmatch(value)
    require(match is not None, "Only stable vMAJOR.MINOR.PATCH releases are supported.")
    result = tuple(int(part) for part in match.groups())
    require(all(part <= 2147483647 for part in result), "Version component is too large.")
    return result


def repository(value):
    require(isinstance(value, str) and len(value) <= 201, "Invalid GitHub owner/repo.")
    parts = value.split("/")
    require(len(parts) == 2, "Use GitHub owner/repo, not a URL.")
    owner, repo = parts
    require(re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?", owner, re.ASCII) is not None,
            "Invalid GitHub owner.")
    require(re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?", repo, re.ASCII) is not None,
            "Invalid GitHub repository.")
    return value


def listener(value, public=False):
    address = r"0\.0\.0\.0" if public else r"127\.0\.0\.1"
    require(isinstance(value, str) and re.fullmatch(address + r":([1-9][0-9]{3,4})", value, re.ASCII),
            "监听地址必须与访问模式一致，端口范围 1024–65535。")
    port = int(value.rsplit(":", 1)[1])
    require(1024 <= port <= 65535, "端口范围为 1024–65535。")
    return port


def canonical_host(value, ipv4_only=False):
    require(isinstance(value, str) and value.isascii() and 1 <= len(value) <= 253, "主机必须是 IPv4 或 ASCII 域名。")
    try:
        address = ipaddress.IPv4Address(value)
    except ValueError:
        require(not ipv4_only and not re.fullmatch(r"[0-9.]+", value), "自签公网模式必须输入全局可路由 IPv4。")
        host = value.lower()
        require("." in host and not host.endswith(".") and all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", part, re.ASCII)
                for part in host.split(".")), "域名格式无效。")
        require(not host.endswith((".localhost", ".local")), "公网地址不能使用本机域名。")
        return host
    require(str(address) == value and address.is_global and not address.is_multicast and not address.is_reserved
            and address not in ipaddress.IPv4Network("192.0.0.0/24")
            and address not in ipaddress.IPv4Network("192.88.99.0/24"), "请确认全局可路由 IPv4；私网、本机、文档和保留地址不用于公网安装。")
    return str(address)


def access_settings(mode, port=None, host=None, external_origin=None):
    require(mode in ACCESS_MODES, "访问方式不受支持。")
    public = mode.startswith("public-")
    port = port if port is not None else (8443 if public else 5173)
    require(integer(port, 1024, 65535), "端口范围为 1024–65535。")
    listen = ("0.0.0.0:" if public else "127.0.0.1:") + str(port)
    if public:
        require(external_origin is None, "公网地址由 --host 和 --port 生成，不能同时指定 --origin。")
        host = canonical_host(host, ipv4_only=mode == "public-selfsigned")
        netloc = host + (":" + str(port) if port != 443 else "")
        normalized = "https://" + netloc
    else:
        require(host is None, "本机和代理模式不接受 --host。")
        require((mode == "proxy" and bool(external_origin)) or (mode == "local" and not external_origin),
                "proxy 必须提供已有 HTTPS 代理地址；local 不接受外部地址。")
        normalized, netloc = origin(external_origin or "", listen)
    return {"accessMode": mode, "listen": listen, "origin": normalized, "healthHost": netloc, "tls": None}


def tls_metadata(value):
    fields(value, ("generation", "host", "fingerprint", "notAfter"))
    require(isinstance(value["generation"], str) and re.fullmatch(r"[0-9a-f]{32}", value["generation"], re.ASCII), "证书 generation 无效。")
    require(isinstance(value["fingerprint"], str) and HEX.fullmatch(value["fingerprint"]), "证书 fingerprint 无效。")
    release_date(value["notAfter"])
    require(canonical_host(value["host"]) == value["host"], "证书主机不是规范 IPv4 / 域名。")
    return value


def access_config(value):
    mode = value["accessMode"]
    public = isinstance(mode, str) and mode.startswith("public-")
    port = listener(value["listen"], public=public)
    if public:
        tls = tls_metadata(value["tls"])
        settings = access_settings(mode, port, tls["host"])
    else:
        require(value["tls"] is None, "本机和代理模式不得配置 TLS generation。")
        settings = access_settings(mode, port, external_origin=value["origin"] or None)
    require(all(value[key] == settings[key] for key in ("listen", "origin", "healthHost")), "访问模式、监听地址与 origin / healthHost 不一致。")


def config2(old, digest=None):
    new = dict(old)
    if old["format"] == 1:
        new.update(format=2, accessMode="proxy" if old["origin"] else "local", tls=None)
    if digest is not None:
        new["installerSHA256"] = digest
    return new


def origin(value, listen=LISTEN):
    listener(listen)
    if value == "":
        return "", listen
    require(isinstance(value, str) and len(value) <= 300 and value.isascii(), "Origin must be an HTTPS ASCII domain.")
    require(not any(ord(char) <= 32 or ord(char) == 127 for char in value) and "%" not in value,
            "Invalid characters in origin.")
    try:
        parsed = urllib.parse.urlsplit(value)
        host, port = parsed.hostname, parsed.port
    except ValueError as exc:
        raise Failure("Invalid HTTPS origin.") from exc
    require(parsed.scheme == "https" and not parsed.username and not parsed.password and "@" not in parsed.netloc,
            "Origin must use HTTPS without credentials.")
    require(parsed.path in ("", "/") and not parsed.query and not parsed.fragment and "?" not in value and "#" not in value,
            "Origin cannot contain a path, query or fragment.")
    require(host is not None and len(host) <= 253 and "." in host and not host.endswith("."),
            "Origin requires a domain name, not an IP or localhost.")
    for label in host.split("."):
        require(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label, re.ASCII) is not None,
                "Invalid origin domain.")
    try:
        ipaddress.ip_address(host)
    except ValueError:
        pass
    else:
        raise Failure("Origin requires a domain name, not an IP address.")
    require(not parsed.netloc.endswith(":"), "Origin has an empty port.")
    require(port is None or 1 <= port <= 65535, "Invalid origin port.")
    netloc = host + (":" + str(port) if port not in (None, 443) else "")
    return "https://" + netloc, netloc


def strict_json(raw, limit):
    require(isinstance(raw, bytes) and len(raw) <= limit, "JSON exceeds its size limit.")

    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, "Duplicate JSON field.")
            result[key] = value
        return result

    def constant(_):
        raise Failure("Non-finite JSON numbers are forbidden.")

    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=pairs, parse_constant=constant)
    except (ValueError, UnicodeError, RecursionError) as exc:
        raise Failure("Invalid UTF-8 JSON.") from exc
    require(type(value) is dict, "Expected a JSON object.")
    return value


def fields(value, expected):
    require(type(value) is dict and set(value) == set(expected), "Unexpected or missing metadata fields.")


def release_date(value):
    require(isinstance(value, str) and re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", value, re.ASCII),
            "Build time must be UTC YYYY-MM-DDTHH:MM:SSZ.")
    try:
        datetime.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError as exc:
        raise Failure("Invalid build time.") from exc


def canonical(repo, tag, name):
    repository(repo)
    version_tuple(tag, tagged=True)
    require(name in NAMES, "Unexpected release asset name.")
    return "https://github.com/" + repo + "/releases/download/" + tag + "/" + name


def validate_url(url, initial, api=False):
    require(isinstance(url, str) and len(url) <= 8192 and url.isascii()
            and not any(ord(char) <= 32 or ord(char) == 127 for char in url), "Invalid download URL.")
    try:
        parsed = urllib.parse.urlsplit(url)
        port = parsed.port
    except ValueError as exc:
        raise Failure("Invalid download URL port.") from exc
    require(parsed.scheme == "https" and parsed.hostname is not None and port in (None, 443)
            and not parsed.username and not parsed.password and "@" not in parsed.netloc
            and not parsed.fragment and "#" not in url, "Downloads require HTTPS without credentials or fragments.")
    if api:
        require(url == initial and parsed.netloc == "api.github.com", "GitHub API redirects are forbidden.")
    else:
        require(parsed.hostname in CDN_HOSTS, "Download redirect is outside the GitHub CDN allowlist.")
        if parsed.hostname == "github.com":
            require(url == initial, "GitHub asset path differs from the canonical release asset.")


class Redirects(urllib.request.HTTPRedirectHandler):
    def __init__(self, initial, api):
        self.initial, self.api, self.count = initial, api, 0

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        self.count += 1
        require(not self.api and self.count <= 5, "Too many redirects or forbidden API redirect.")
        validate_url(newurl, self.initial, self.api)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


@contextlib.contextmanager
def deadline(seconds):
    require(seconds > 0, "Operation deadline expired.")
    previous = signal.getsignal(signal.SIGALRM)

    def expired(_signum, _frame):
        raise Failure("Operation exceeded its time limit.")

    signal.signal(signal.SIGALRM, expired)
    signal.setitimer(signal.ITIMER_REAL, seconds)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous)


def system_ca_file():
    # Fixed distro CA locations only; no inherited SSL_CERT_FILE or custom trust.
    for path in ("/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt"):
        if os.path.isfile(path) and os.path.getsize(path) > 0:
            return path
    raise Failure("No supported system CA bundle is available.")


class Downloads:
    # Dependency injection is an import-only testing API, never a CLI/environment option.
    def __init__(self, opener=None, timer=deadline):
        self.opener, self.timer = opener, timer

    def get(self, url, limit, timeout, destination=None, api=False):
        validate_url(url, url, api)
        require(0 < timeout <= (15 if api else 120), "Invalid network deadline.")
        handler = Redirects(url, api)
        opener = self.opener or urllib.request.build_opener(urllib.request.ProxyHandler({}), handler,
                    urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=system_ca_file())))
        request = urllib.request.Request(url, headers={
            "Accept": "application/vnd.github+json" if api else "application/octet-stream",
            "Accept-Encoding": "identity", "User-Agent": "server-panel-installer/1",
        })
        digest, total, data = hashlib.sha256(), 0, bytearray()
        output = None
        try:
            if destination is not None:
                fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
                output = os.fdopen(fd, "wb")
            with self.timer(timeout), opener.open(request, timeout=min(timeout, 15)) as response:
                require(response.status == 200, "GitHub returned an unexpected HTTP status.")
                validate_url(response.geturl(), url, api)
                require(response.headers.get("Content-Encoding", "identity").lower() == "identity", "Encoded release transfers are forbidden.")
                length = response.headers.get("Content-Length")
                if length is not None:
                    require(re.fullmatch(r"[0-9]{1,12}", length, re.ASCII) is not None and int(length) <= limit,
                            "Download exceeds its size limit.")
                end = time.monotonic() + timeout
                while True:
                    require(time.monotonic() < end, "Download exceeded its time limit.")
                    chunk = response.read1(min(65536, limit + 1 - total))
                    if not chunk:
                        break
                    total += len(chunk)
                    require(total <= limit, "Download exceeds its size limit.")
                    digest.update(chunk)
                    if output is None:
                        data.extend(chunk)
                    else:
                        output.write(chunk)
                require(length is None or total == int(length), "Incomplete download.")
            if output is not None:
                output.flush()
                os.fsync(output.fileno())
                return total, digest.hexdigest()
            return bytes(data)
        except (urllib.error.URLError, OSError, http.client.HTTPException) as exc:
            raise Failure("GitHub HTTPS transfer failed; TLS verification was not disabled.") from exc
        finally:
            if output is not None:
                output.close()


def asset_limit(name):
    if name in {target[1] for target in BINARY_TARGETS.values()}:
        return ARCHIVE_LIMIT
    return {"release.json": MANIFEST_LIMIT, "SHA256SUMS": SUMS_LIMIT,
            "install.sh": SCRIPT_LIMIT, "install.ps1": SCRIPT_LIMIT,
            "update.sh": WRAPPER_LIMIT, "update.ps1": WRAPPER_LIMIT}[name]


def release_api(raw, repo, requested, legacy=False):
    value = strict_json(raw, API_LIMIT)
    require(value.get("draft") is False and value.get("prerelease") is False, "A published stable release is required.")
    tag = value.get("tag_name")
    version_tuple(tag, tagged=True)
    require(requested is None or tag == requested, "GitHub returned the wrong release tag.")
    require(value.get("html_url") == "https://github.com/" + repo + "/releases/tag/" + tag,
            "GitHub returned a non-canonical Release URL.")
    assets = value.get("assets")
    expected = LEGACY_NAMES if legacy else NAMES
    require(type(assets) is list and len(assets) == len(expected),
            "Release must contain the contracted " + ("six legacy" if legacy else "nine format 3") + " assets; new management requires >=v0.5.0 format 3.")
    sizes = {}
    for asset in assets:
        require(type(asset) is dict and isinstance(asset.get("name"), str), "Invalid release asset.")
        name = asset["name"]
        require(name in expected and name not in sizes, "Unknown or duplicate release asset.")
        require(integer(asset.get("size"), 1, asset_limit(name)), "Invalid release asset size.")
        require(asset.get("browser_download_url") == canonical(repo, tag, name), "Non-canonical asset download URL.")
        require(asset.get("state", "uploaded") == "uploaded", "A release asset is not fully uploaded.")
        sizes[name] = asset["size"]
    require(set(sizes) == expected, "Missing release assets.")
    return tag, sizes


def checksums(raw, legacy=False):
    require(isinstance(raw, bytes) and 0 < len(raw) <= SUMS_LIMIT and raw.endswith(b"\n"), "Invalid SHA256SUMS.")
    try:
        lines = raw.decode("ascii").splitlines(keepends=True)
    except UnicodeError as exc:
        raise Failure("SHA256SUMS must be ASCII.") from exc
    expected = (LEGACY_NAMES if legacy else NAMES) - {"SHA256SUMS"}
    result = {}
    for line in lines:
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_.-]+)\n", line, re.ASCII)
        require(match is not None, "Invalid SHA256SUMS line.")
        digest, name = match.groups()
        require(name in expected and name not in result, "Unknown or duplicate checksum asset.")
        result[name] = digest
    require(set(result) == expected, "SHA256SUMS does not cover exactly the expected assets.")
    return result


def manifest(raw, sums, repo, tag, legacy=False):
    require(hashlib.sha256(raw).hexdigest() == sums["release.json"], "Manifest SHA-256 mismatch.")
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("format", "version", "tag", "repository", "dataSchema", "commit", "buildTime", "binaries"))
    expected_format = 2 if legacy else 3
    require(integer(value["format"], expected_format, expected_format) and integer(value["dataSchema"], 1, 1),
            "Only manifest format " + str(expected_format) + " and data schema 1 are supported for this operation.")
    version_tuple(value["version"])
    require(value["tag"] == tag and value["version"] == tag[1:] and value["repository"] == repo, "Manifest release identity mismatch.")
    require(isinstance(value["commit"], str) and COMMIT.fullmatch(value["commit"]), "Invalid release commit.")
    release_date(value["buildTime"])
    binaries = value["binaries"]
    targets = {key: target for key, target in BINARY_TARGETS.items() if not legacy or key[0] == "linux"}
    require(type(binaries) is list and len(binaries) == len(targets), "Manifest must describe exactly the contracted binary records.")
    seen = set()
    for binary in binaries:
        fields(binary, ("os", "arch", "name", "size", "sha256", "archive"))
        require(isinstance(binary["os"], str) and isinstance(binary["arch"], str), "Invalid manifest platform.")
        key = binary["os"], binary["arch"]
        require(key in targets and key not in seen, "Invalid or duplicate manifest platform.")
        seen.add(key)
        binary_name, archive_name = targets[key]
        require(binary["name"] == binary_name, "Invalid inner binary identity.")
        require(integer(binary["size"], 1, BINARY_LIMIT), "Invalid binary size.")
        require(isinstance(binary["sha256"], str) and HEX.fullmatch(binary["sha256"]), "Invalid inner binary SHA-256.")
        archive = binary["archive"]
        fields(archive, ("name", "size", "sha256"))
        require(archive["name"] == archive_name, "Invalid archive asset identity.")
        require(integer(archive["size"], 1, ARCHIVE_LIMIT), "Invalid archive size.")
        require(isinstance(archive["sha256"], str) and HEX.fullmatch(archive["sha256"])
                and archive["sha256"] == sums[archive_name], "Archive manifest/checksum mismatch.")
    require(seen == set(targets), "Missing manifest binary record.")
    return value


def gzip_chunks(stream):
    # zlib's max_length bounds allocation BEFORE decompression. gzip.GzipFile also
    # accepts concatenated members, which are deliberately not part of format 2.
    decoder = zlib.decompressobj(16 + zlib.MAX_WBITS)
    compressed = expanded = 0
    try:
        while True:
            chunk = stream.read(min(65536, ARCHIVE_LIMIT + 1 - compressed))
            if not chunk:
                break
            compressed += len(chunk)
            require(compressed <= ARCHIVE_LIMIT, "Archive exceeds its compressed size limit.")
            while chunk:
                raw = decoder.decompress(chunk, min(65536, TAR_LIMIT + 1 - expanded))
                expanded += len(raw)
                require(expanded <= TAR_LIMIT, "Archive expansion exceeds its tar size limit.")
                if raw:
                    yield raw
                if decoder.eof:
                    require(not decoder.unused_data and not stream.read(1), "Trailing data or multiple gzip members are forbidden.")
                    return
                chunk = decoder.unconsumed_tail
        require(decoder.eof, "Truncated gzip archive.")
    except zlib.error as exc:
        raise Failure("Invalid gzip archive integrity.") from exc


def extract_panel(stream, candidate, expected):
    # Parse exactly one raw tar header, not TarFile's extension-aware iterator.
    # Never use an archive-supplied path to open a file. The caller supplies the
    # fixed staging path, created exclusively and non-executable until verified.
    header, digest = bytearray(), hashlib.sha256()
    remaining, padding, trailer, output = None, 0, 0, None
    written = 0
    try:
        for chunk in gzip_chunks(stream):
            if remaining is None:
                count = min(512 - len(header), len(chunk))
                header.extend(chunk[:count])
                chunk = chunk[count:]
                if len(header) < 512:
                    continue
                raw_header = bytes(header)
                try:
                    member = tarfile.TarInfo.frombuf(raw_header, "ascii", "strict")
                except (tarfile.TarError, ValueError, UnicodeError) as exc:
                    raise Failure("Invalid tar header.") from exc
                require(raw_header[:100] == b"panel" + b"\0" * 95 and member.name == "panel"
                        and raw_header[345:500] == b"\0" * 155, "Archive must contain only the top-level panel path.")
                require(member.type == tarfile.REGTYPE and member.mode == 0o755
                        and raw_header[157:257] == b"\0" * 100, "Archive panel must be a mode 0755 regular file, not a link or extension.")
                require(raw_header[257:265] == b"ustar\00000", "Only a plain USTAR header is supported.")
                require(integer(member.size, 1, BINARY_LIMIT) and member.size == expected["size"], "Inner binary tar/manifest size mismatch.")
                remaining, padding = member.size, (-member.size) % 512
                fd = os.open(candidate, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
                output = os.fdopen(fd, "wb")
            count = min(remaining, len(chunk))
            if count:
                output.write(chunk[:count])
                digest.update(chunk[:count])
                written += count
                remaining -= count
                chunk = chunk[count:]
            if chunk:
                count = min(padding, len(chunk))
                require(not any(chunk[:count]), "Nonzero tar file padding is forbidden.")
                padding -= count
                chunk = chunk[count:]
                require(not any(chunk), "Extra tar members or trailing nonzero data are forbidden.")
                trailer += len(chunk)
        require(output is not None and remaining == 0 and padding == 0
                and trailer >= 1024 and trailer % 512 == 0, "Truncated tar archive or missing end blocks.")
        require(written == expected["size"] and digest.hexdigest() == expected["sha256"], "Inner binary SHA-256/size mismatch.")
        output.flush()
        os.fsync(output.fileno())
        return written, digest.hexdigest()
    finally:
        if output is not None:
            output.close()


def build_info(raw, repo, arch, expected=None):
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("version", "commit", "buildTime", "repository", "dataSchema", "os", "arch"))
    version_tuple(value["version"])
    require(integer(value["dataSchema"], 1, 1) and value["os"] == "linux" and value["arch"] == arch
            and value["repository"] == repo, "Binary build-info repository/platform/schema mismatch.")
    require(isinstance(value["commit"], str) and COMMIT.fullmatch(value["commit"]), "Invalid binary commit.")
    release_date(value["buildTime"])
    if expected is not None:
        for key in ("version", "commit", "buildTime", "repository", "dataSchema"):
            require(value[key] == expected[key], "Binary build-info differs from verified release metadata.")
    return value


class Paths:
    binary = Path("/usr/local/bin/panel")
    previous = Path("/usr/local/bin/panel.previous")
    code_dir = Path("/usr/local/lib/server-panel")
    installer = code_dir / "install.sh"
    manager_previous = code_dir / "install.sh.v0.4.0.previous"
    manager_v05_previous = code_dir / "install.sh.v0.5.0.previous"
    updater = Path("/usr/local/bin/panel-update")
    qqon = Path("/usr/local/bin/qqon")
    config_dir = Path("/etc/server-panel")
    tls_root = config_dir / "tls"
    config = config_dir / "install.json"
    transaction = config_dir / "update-transaction.json"
    migration = config_dir / "manager-migration.json"
    access_transaction = config_dir / "access-transaction.json"
    backups = Path("/var/backups/server-panel")
    unit = Path("/etc/systemd/system/server-panel.service")
    data = Path("/var/lib/server-panel")
    lock_dir = Path("/run/server-panel-update")
    lock = lock_dir / "lock"


class SafeFiles:
    def info(self, path):
        return os.lstat(path)

    def source_parents(self, path):
        for parent in Path(path).parents:
            entry = self.info(parent)
            require(stat.S_ISDIR(entry.st_mode) and not stat.S_ISLNK(entry.st_mode), "导入来源路径不可经过符号链接。")

    def owner_mode(self, path, uid, gid, mode):
        os.chown(path, uid, gid, follow_symlinks=False)
        os.chmod(path, mode, follow_symlinks=False)

    def parents(self, path):
        for parent in reversed(Path(path).parents):
            entry = self.info(parent)
            require(stat.S_ISDIR(entry.st_mode) and entry.st_uid == 0 and not entry.st_mode & 0o022,
                    "A managed path has a symlink, non-root owner or writable parent: " + str(parent))

    def check(self, path, directory=False, mode=None, uid=0, gid=0):
        self.parents(path)
        entry = self.info(path)
        expected_type = stat.S_ISDIR if directory else stat.S_ISREG
        require(expected_type(entry.st_mode) and not stat.S_ISLNK(entry.st_mode) and entry.st_uid == uid and entry.st_gid == gid,
                "Unsafe type or owner at " + str(path))
        require(not entry.st_mode & 0o022 and (mode is None or stat.S_IMODE(entry.st_mode) == mode), "Unsafe permissions at " + str(path))
        require(directory or entry.st_nlink == 1, "Hard-linked management files are forbidden: " + str(path))
        return entry

    def exists(self, path):
        return os.path.lexists(path)

    def mkdir(self, path, mode=0o755, uid=0, gid=0):
        self.parents(path)
        if self.exists(path):
            self.check(path, directory=True, mode=mode, uid=uid, gid=gid)
            return
        os.mkdir(path, 0o700)
        self.owner_mode(path, uid, gid, mode)
        self.check(path, directory=True, mode=mode, uid=uid, gid=gid)

    def read(self, path, limit, mode=None, uid=0, gid=0):
        entry = self.check(path, mode=mode, uid=uid, gid=gid)
        require(entry.st_size <= limit, "Management file exceeds size limit: " + str(path))
        fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(fd, "rb") as stream:
            opened = os.fstat(stream.fileno())
            require((opened.st_dev, opened.st_ino) == (entry.st_dev, entry.st_ino), "Management file changed while opening.")
            raw = stream.read(limit + 1)
        require(len(raw) <= limit, "Management file exceeds size limit.")
        return raw

    def sync_dir(self, path):
        fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    def replace(self, source, target):
        self.parents(target)
        if self.exists(target):
            self.check(target)
        self.check(source)
        require(self.info(source).st_dev == self.info(target.parent).st_dev, "Atomic replacement requires the same filesystem.")
        # Flush both file data and its final ownership/mode before publishing the rename.
        fd = os.open(source, os.O_RDWR | getattr(os, "O_NOFOLLOW", 0))
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
        os.replace(source, target)
        self.sync_dir(target.parent)

    def atomic_bytes(self, path, data, mode):
        self.parents(path)
        if self.exists(path):
            self.check(path, mode=mode)
        fd, name = tempfile.mkstemp(prefix=".panel-write-", dir=path.parent)
        temporary = Path(name)
        try:
            with os.fdopen(fd, "wb") as stream:
                stream.write(data)
                stream.flush()
                self.owner_mode(temporary, 0, 0, mode)
                os.fsync(stream.fileno())
            self.replace(temporary, path)
        finally:
            if self.exists(temporary):
                os.unlink(temporary)

    def copy_binary(self, source, destination, expected_hash):
        raw = self.read(source, BINARY_LIMIT, mode=0o755)
        require(hashlib.sha256(raw).hexdigest() == expected_hash, "Current/previous binary SHA-256 mismatch.")
        require(not self.exists(destination), "Staging destination already exists.")
        self.atomic_bytes(destination, raw, 0o755)

    @contextlib.contextmanager
    def staging(self, parent):
        self.parents(parent / "unused")
        directory = Path(tempfile.mkdtemp(prefix=".server-panel-stage-", dir=parent))
        self.owner_mode(directory, 0, 0, 0o700)
        try:
            yield directory
        finally:
            # Only our freshly created flat staging directory is removed, never panel data.
            for name in directory.iterdir():
                if name.is_file() or name.is_symlink():
                    name.unlink()
            directory.rmdir()


def legacy_unit_bytes(config):
    # Exact v0.4.0 template; keep these bytes for the pinned-manager migration.
    repo = repository(config["repository"])
    normalized, host = origin(config["origin"])
    require(normalized == config["origin"] and host == config["healthHost"], "Non-normalized installation origin.")
    command = "/usr/local/bin/panel -listen 127.0.0.1:5173 -data-dir /var/lib/server-panel -github-repo " + repo
    if normalized:
        command += " -origin " + normalized
    require("%" not in command and "\n" not in command, "Invalid unit arguments.")
    return ("""[Unit]
Description=Single-process server panel
After=network.target

[Service]
Type=simple
User=server-panel
Group=server-panel
StateDirectory=server-panel
StateDirectoryMode=0700
WorkingDirectory=/var/lib/server-panel
ExecStart=""" + command + """
Restart=on-failure
RestartSec=5s
TimeoutStartSec=30s
TimeoutStopSec=15s
UMask=0077
Nice=10
CPUWeight=20
IOWeight=20
MemoryHigh=80M
MemoryMax=128M
TasksMax=96
LimitNOFILE=512
Environment=GOMEMLIMIT=64MiB
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
RestrictRealtime=true
RestrictNamespaces=true
CapabilityBoundingSet=
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

[Install]
WantedBy=multi-user.target
""").encode("utf-8")


def tls_paths(config, paths=None):
    paths = paths or Paths()
    tls = tls_metadata(config["tls"])
    directory = paths.tls_root / tls["generation"]
    return directory / "cert.pem", directory / "key.pem"


def unit_bytes(config):
    listen = config["listen"]
    public = config.get("accessMode", "").startswith("public-")
    if config.get("format") == 2:
        access_config(config)
    listener(listen, public=public)
    if public:
        legacy = dict(config, listen=LISTEN, origin="", healthHost=LISTEN)
        raw = legacy_unit_bytes(legacy)
        cert, key = tls_paths(config)
        command = " -origin " + config["origin"] + " -tls-cert " + cert.as_posix() + " -tls-key " + key.as_posix()
        raw = raw.replace(b" -github-repo " + config["repository"].encode("ascii") + b"\n",
                          b" -github-repo " + config["repository"].encode("ascii") + command.encode("ascii") + b"\n", 1)
    else:
        normalized, host = origin(config["origin"], listen)
        require(normalized == config["origin"] and host == config["healthHost"], "Non-normalized installation origin.")
        legacy = dict(config, listen=LISTEN, healthHost=origin(normalized)[1])
        raw = legacy_unit_bytes(legacy)
    return raw.replace(b"-listen 127.0.0.1:5173 ", b"-listen " + listen.encode("ascii") + b" ", 1)


def config_bytes(config):
    return (json.dumps(config, ensure_ascii=True, indent=2, sort_keys=True) + "\n").encode("utf-8")


def installation_config(raw):
    value = strict_json(raw, MANIFEST_LIMIT)
    expected = ("format", "repository", "listen", "origin", "healthHost", "version", "dataSchema", "commit", "buildTime",
                "binarySHA256", "installerSHA256")
    require(integer(value.get("format"), 1, 2), "不支持的安装记录 format；只接受 1 或 2。")
    fields(value, expected + (("accessMode", "tls") if value["format"] == 2 else ()))
    require(integer(value["dataSchema"], 1, 1), "Unsupported installation format/data schema.")
    repository(value["repository"])
    version_tuple(value["version"])
    if value["format"] == 2:
        access_config(value)
    else:
        listener(value["listen"])
        normalized, host = origin(value["origin"], value["listen"])
        require(normalized == value["origin"] and host == value["healthHost"], "Installation origin/health Host mismatch.")
    for key in ("binarySHA256", "installerSHA256"):
        require(isinstance(value[key], str) and HEX.fullmatch(value[key]), "Invalid installation SHA-256.")
    require(isinstance(value["commit"], str) and COMMIT.fullmatch(value["commit"]), "Invalid installed commit.")
    release_date(value["buildTime"])
    return value


def update_transaction(raw):
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("format", "phase", "old", "new"))
    require(integer(value["format"], 1, 1) and value["phase"] in ("prepared", "ready"), "Invalid update transaction format/phase.")
    old = installation_config(config_bytes(value["old"]))
    new = installation_config(config_bytes(value["new"]))
    for key in ("format", "repository", "listen", "origin", "healthHost", "dataSchema", "installerSHA256"):
        require(old[key] == new[key], "Update transaction changes fixed installation settings.")
    if old["format"] == 2:
        require(old["accessMode"] == new["accessMode"] and old["tls"] == new["tls"], "升级事务不得改变访问方式或证书代际。")
    require(version_tuple(new["version"]) > version_tuple(old["version"])
            and old["binarySHA256"] != new["binarySHA256"], "Invalid update transaction version/digest transition.")
    return value


def manager_migration(raw):
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("format", "phase", "old", "new"))
    require(integer(value["format"], 1, 2) and value["phase"] in ("prepared", "ready"), "Invalid manager migration phase/format.")
    old = installation_config(config_bytes(value["old"]))
    new = installation_config(config_bytes(value["new"]))
    require(old["installerSHA256"] in legacy_hashes() and old["format"] == 1
            and (old["installerSHA256"] != LEGACY_INSTALLER_SHA256 or old["listen"] == LISTEN),
            "只能迁移固定摘要的官方 v0.4/v0.5 管理器。")
    if value["format"] == 1:
        require(old["installerSHA256"] == LEGACY_INSTALLER_SHA256 and new["installerSHA256"] != old["installerSHA256"]
                and dict(new, installerSHA256=old["installerSHA256"]) == old,
                "Manager migration may change only installerSHA256.")
    else:
        require(new["installerSHA256"] not in legacy_hashes() and new == config2(old, new["installerSHA256"]),
                "管理器迁移只允许 format2 扩充和 installerSHA256 变更；旧应用、账户与访问方式不变。")
    return value


def access_transaction(raw):
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("format", "old", "new", "wasRunning"))
    require(integer(value["format"], 1, 1) and type(value["wasRunning"]) is bool, "访问事务格式无效。")
    old = installation_config(config_bytes(value["old"]))
    new = installation_config(config_bytes(value["new"]))
    require(old["format"] == new["format"] == 2, "配置事务仅支持 format2。")
    require({k: v for k, v in old.items() if k not in ACCESS_FIELDS} == {k: v for k, v in new.items() if k not in ACCESS_FIELDS},
            "配置事务不得改变应用身份、管理器摘要或账户 schema。")
    if new["tls"] is not None:
        require(old["tls"] is None or new["tls"]["generation"] != old["tls"]["generation"], "新证书必须使用全新 generation。")
    require(new != old, "访问配置没有变化。")
    return value


class PinnedHTTPSConnection(http.client.HTTPSConnection):
    def __init__(self, config, cert_path, timeout):
        tls = config["tls"]
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_verify_locations(cafile=str(cert_path))
        if hasattr(ssl, "VERIFY_X509_PARTIAL_CHAIN"):
            context.verify_flags |= ssl.VERIFY_X509_PARTIAL_CHAIN
        context.check_hostname = True
        context.hostname_checks_common_name = False
        self.fingerprint = tls["fingerprint"]
        self.expected_not_after = tls["notAfter"]
        super().__init__(tls["host"], listener(config["listen"], public=True), timeout=timeout, context=context)

    def connect(self):
        raw = socket.create_connection(("127.0.0.1", self.port), self.timeout)
        try:
            self.sock = self._context.wrap_socket(raw, server_hostname=self.host)
            leaf = self.sock.getpeercert(binary_form=True)
            require(hashlib.sha256(leaf).hexdigest() == self.fingerprint, "HTTPS leaf fingerprint 不匹配。")
            info = self.sock.getpeercert()
            require(bool(info.get("subjectAltName")), "HTTPS 证书缺少 SAN。")
            expires = ssl.cert_time_to_seconds(info["notAfter"])
            expected = datetime.datetime.strptime(self.expected_not_after, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.timezone.utc).timestamp()
            require(expires == expected and expires > time.time(), "HTTPS 证书已过期或有效期与记录不一致。")
        except BaseException:
            raw.close()
            self.close()
            raise


class Runtime:
    def preflight(self, files):
        require(sys.platform == "linux" and os.geteuid() == 0, "Management requires root on systemd Linux.")
        arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine())
        require(arch is not None, "Only Linux amd64/arm64 are supported.")
        require(sys.version_info >= (3, 8), "Python 3.8 or newer is required after dependency bootstrap.")
        files.check(Path("/run/systemd/system"), directory=True)
        try:
            trust = ssl.create_default_context(cafile=system_ca_file())
            require(trust.cert_store_stats()["x509_ca"] > 0, "A usable system CA store is required after dependency bootstrap.")
        except (OSError, ssl.SSLError) as exc:
            raise Failure("A usable system CA store is required after dependency bootstrap.") from exc
        for tool in ("/usr/bin/systemctl", "/usr/sbin/useradd", "/usr/sbin/groupadd", "/usr/sbin/nologin"):
            entry = files.check(Path(tool))
            require(entry.st_mode & 0o111, "Required tool is not executable: " + tool)
        return arch

    def run(self, arguments, timeout=25, output_limit=65536):
        # No shell, inherited environment, unbounded stdout or orphan process on timeout.
        process = subprocess.Popen([str(part) for part in arguments], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, cwd="/", env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LANG": "C", "LC_ALL": "C", "HOME": "/"},
                                   close_fds=True, start_new_session=True)
        stdout, stderr = bytearray(), bytearray()
        end = time.monotonic() + timeout
        selector = selectors.DefaultSelector()
        selector.register(process.stdout, selectors.EVENT_READ, stdout)
        selector.register(process.stderr, selectors.EVENT_READ, stderr)
        try:
            while selector.get_map():
                remaining = end - time.monotonic()
                require(remaining > 0, "A management subprocess timed out.")
                for key, _ in selector.select(min(remaining, 0.5)):
                    chunk = os.read(key.fileobj.fileno(), 8192)
                    if chunk:
                        key.data.extend(chunk)
                        require(len(stdout) + len(stderr) <= output_limit, "Management subprocess output limit exceeded.")
                    else:
                        selector.unregister(key.fileobj)
            remaining = end - time.monotonic()
            require(remaining > 0, "A management subprocess timed out.")
            code = process.wait(timeout=remaining)
            return code, bytes(stdout)
        except BaseException:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
            raise
        finally:
            selector.close()
            process.stdout.close()
            process.stderr.close()

    def systemctl(self, verb, *options, timeout=25, checked=True):
        show_options = ("--property=LoadState", "--property=FragmentPath", "--property=DropInPaths", "--no-pager")
        state_options = ("--property=ActiveState", "--property=SubState", "--no-pager")
        require((verb == "show" and options in (show_options, state_options))
                or (verb == "is-active" and options == ("--quiet",))
                or (verb in {"stop", "start", "restart", "enable", "disable", "daemon-reload"} and not options),
                "服务命令或参数不在固定白名单内。")
        arguments = ["/usr/bin/systemctl", verb]
        if verb != "daemon-reload":
            arguments.extend(options)
            arguments.append(UNIT)
        code, output = self.run(arguments, timeout)
        require(not checked or code == 0, "systemctl " + verb + " failed.")
        return code, output

    def service_state(self):
        code, raw = self.systemctl("show", "--property=ActiveState", "--property=SubState", "--no-pager", checked=False)
        require(code == 0, "无法读取固定服务的实际状态；未进行变更。")
        try:
            value = dict(line.split("=", 1) for line in raw.decode("ascii").strip().splitlines())
        except (ValueError, UnicodeError) as exc:
            raise Failure("服务状态输出无效。") from exc
        require(set(value) == {"ActiveState", "SubState"} and all(re.fullmatch(r"[a-z-]{1,32}", item) for item in value.values()), "服务状态字段无效。")
        return value

    def running(self):
        state = self.service_state()
        require(state["ActiveState"] in ("active", "inactive", "failed"), "服务正在切换状态，请稍后再维护。")
        return state["ActiveState"] == "active"

    def stop_wait(self):
        self.systemctl("stop")
        end = time.monotonic() + 15
        while time.monotonic() < end:
            if self.service_state()["ActiveState"] in ("inactive", "failed"):
                return
            time.sleep(0.2)
        raise Failure("停服等待超时；未取得一致快照。")

    def logs(self, lines):
        require(integer(lines, 1, 200), "日志行数范围为 1–200。")
        code, raw = self.run(["/usr/bin/journalctl", "--unit=" + UNIT, "--lines=" + str(lines), "--no-pager", "--output=cat", "--quiet"],
                             timeout=10, output_limit=LOG_LIMIT)
        require(code == 0 and len(raw) <= LOG_LIMIT, "读取本服务日志失败或超过 128 KiB。")
        safe = []
        for line in raw.decode("utf-8", errors="replace").splitlines()[-lines:]:
            line = "".join(char for char in line if ord(char) >= 32 and ord(char) != 127)
            if re.search(r"token|password|passwd|secret|authorization|cookie|private[ -]?key|令牌|密码|私钥|-----BEGIN|[A-Za-z0-9_+/=-]{40,}", line, re.I):
                line = "[含凭据或密钥字段的日志已隐藏]"
            safe.append(line[:8192])
        print("\n".join(safe).encode("utf-8")[:LOG_LIMIT].decode("utf-8", errors="ignore"))

    def tls_info(self, binary, host, output=None, cert=None, key=None):
        if output is not None:
            args = [binary, "-tls-create", "-tls-host", host, "-tls-output", str(output)]
            cert, key = output / "cert.pem", output / "key.pem"
        else:
            args = [binary, "-tls-inspect", "-tls-host", host, "-tls-cert", str(cert), "-tls-key", str(key)]
        code, raw = self.run(args, timeout=15)
        require(code == 0, "证书 CLI 校验或生成失败；未泄露密钥，也未切换访问配置。")
        info = strict_json(raw, MANIFEST_LIMIT)
        fields(info, ("host", "fingerprint", "notAfter", "certFile", "keyFile"))
        require(info["host"] == host and info["certFile"] == str(cert) and info["keyFile"] == str(key), "证书 CLI 返回的主机或文件路径不匹配。")
        require(isinstance(info["fingerprint"], str) and HEX.fullmatch(info["fingerprint"]), "证书 CLI 摘要格式无效。")
        release_date(info["notAfter"])
        require(info["notAfter"] > datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "证书已过期。")
        return info

    def inspect_unit(self, expected):
        _, raw = self.systemctl("show", "--property=LoadState", "--property=FragmentPath", "--property=DropInPaths", "--no-pager", checked=False)
        try:
            entries = dict(line.split("=", 1) for line in raw.decode("utf-8").strip().splitlines())
        except (ValueError, UnicodeError) as exc:
            raise Failure("Unable to inspect the fixed service unit.") from exc
        require(set(entries) == {"LoadState", "FragmentPath", "DropInPaths"} and not entries["DropInPaths"],
                "Unknown service unit overrides are not adopted.")
        if expected is None:
            require(entries["LoadState"] == "not-found" and not entries["FragmentPath"], "An unknown server-panel unit already exists.")
        else:
            require(entries["LoadState"] == "loaded" and entries["FragmentPath"] == str(expected), "The fixed service unit is not loaded from the managed path.")

    def account(self, data, create=False):
        import grp
        import pwd
        try:
            user = pwd.getpwnam(ACCOUNT)
        except KeyError:
            user = None
        try:
            group = grp.getgrnam(ACCOUNT)
        except KeyError:
            group = None
        if create:
            require(user is None and group is None, "An unknown service account/group already exists.")
            code, _ = self.run(["/usr/sbin/groupadd", "--system", ACCOUNT])
            require(code == 0, "Could not create the fixed service group.")
            code, _ = self.run(["/usr/sbin/useradd", "--system", "--gid", ACCOUNT, "--home-dir", str(data),
                                "--no-create-home", "--shell", "/usr/sbin/nologin", "--password", "!", ACCOUNT])
            require(code == 0, "Could not create the fixed service account.")
            user, group = pwd.getpwnam(ACCOUNT), grp.getgrnam(ACCOUNT)
        if user is None and group is None:
            return None
        require(user is not None and group is not None and user.pw_uid > 0 and group.gr_gid > 0
                and user.pw_gid == group.gr_gid and user.pw_shell == "/usr/sbin/nologin" and user.pw_dir == str(data),
                "Unsafe or unknown service account/group.")
        require(set(os.getgrouplist(ACCOUNT, group.gr_gid)) == {group.gr_gid} and not set(group.gr_mem) - {ACCOUNT},
                "The service account must not have supplementary privileges or shared group members.")
        return user.pw_uid, group.gr_gid

    @contextlib.contextmanager
    def lock(self, paths, files):
        import fcntl
        files.mkdir(paths.lock_dir, mode=0o700)
        if files.exists(paths.lock):
            files.check(paths.lock, mode=0o600)
        fd = os.open(paths.lock, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
        try:
            files.check(paths.lock, mode=0o600)
            opened = os.fstat(fd)
            current = files.info(paths.lock)
            require((opened.st_dev, opened.st_ino) == (current.st_dev, current.st_ino), "Update lock changed while opening.")
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as exc:
                raise Failure("Another installation/update holds the independent management lock.") from exc
            yield
        finally:
            os.close(fd)

    def binary_info(self, path):
        code, raw = self.run([path, "-build-info"], timeout=10)
        require(code == 0, "Verified binary -build-info failed.")
        return raw

    def prompt(self, message):
        try:
            with open("/dev/tty", "w", encoding="utf-8") as terminal:
                terminal.write(message)
                terminal.flush()
            with open("/dev/tty", "r", encoding="utf-8") as terminal:
                answer = terminal.readline(4098)
        except OSError as exc:
            raise Failure("没有控制终端；请显式提供参数和 --yes，不能从管道读取确认。") from exc
        require(answer.endswith("\n") and len(answer) <= 4097, "终端输入过长或已结束。")
        return answer.strip()

    def confirm(self, message, unattended):
        print(message, flush=True)
        if unattended:
            return
        require(self.prompt("输入 yes 确认继续，其他输入取消：") == "yes", "已取消；安装状态未更改。")

    def health_once(self, config, timeout):
        connection = None
        try:
            if config.get("tls") is not None:
                cert, _ = tls_paths(config, getattr(self, "paths", None))
                connection = PinnedHTTPSConnection(config, cert, min(2, timeout))
            else:
                connection = http.client.HTTPConnection("127.0.0.1", listener(config["listen"]), timeout=min(2, timeout))
            with deadline(min(2, timeout)):
                connection.request("GET", "/healthz", headers={"Host": config["healthHost"], "Connection": "close"})
                response = connection.getresponse()
                require(response.status == 200, "Health endpoint did not return HTTP 200.")
                value = strict_json(response.read(4097), 4096)
                fields(value, ("status", "version", "dataSchema"))
                return value["status"] == "ok" and value["version"] == config["version"] and integer(value["dataSchema"], 1, 1)
        except (Failure, OSError, http.client.HTTPException):
            return False
        finally:
            if connection is not None:
                connection.close()

    def healthy(self, config):
        end = time.monotonic() + 30
        while time.monotonic() < end:
            if self.health_once(config, end - time.monotonic()):
                code, _ = self.systemctl("is-active", "--quiet", timeout=max(0.01, min(5, end - time.monotonic())), checked=False)
                if code == 0:
                    return
            time.sleep(max(0, min(0.5, end - time.monotonic())))
        raise Failure("Service did not become healthy with the expected version and schema within 30 seconds.")

    @contextlib.contextmanager
    def rollback_signals(self):
        previous = {}
        for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            previous[signum] = signal.signal(signum, signal.SIG_IGN)
        try:
            yield
        finally:
            for signum, handler in previous.items():
                signal.signal(signum, handler)


def read_source(source, installed, files, updating):
    path = Path(os.path.abspath(source))
    if updating:
        require(path == installed, "Update mode may run only from the fixed root-owned installed script.")
        files.check(path, mode=0o755)
    else:
        sudo_uid = os.environ.get("SUDO_UID", "0")
        require(re.fullmatch(r"[0-9]{1,10}", sudo_uid, re.ASCII), "Invalid sudo source identity.")
        allowed = {0, int(sudo_uid)}
        for item in [*reversed(path.parents), path]:
            entry = os.lstat(item)
            require(not stat.S_ISLNK(entry.st_mode) and entry.st_uid in allowed and not entry.st_mode & 0o022,
                    "The reviewed installer must have trusted ownership, no symlinks and no group/world-writable source paths.")
    entry = os.lstat(path)
    opened = os.fstat(4)
    require(stat.S_ISREG(entry.st_mode) and entry.st_nlink == 1 and entry.st_size <= SCRIPT_LIMIT
            and (entry.st_dev, entry.st_ino) == (opened.st_dev, opened.st_ino), "Untrusted installer source descriptor.")
    with os.fdopen(os.dup(4), "rb") as stream:
        raw = stream.read(SCRIPT_LIMIT + 1)
    require(len(raw) <= SCRIPT_LIMIT and raw.startswith(b"#!/bin/bash\n") and raw.endswith(b"PANEL_PYTHON\n"),
            "Installer source must be this complete reviewed Bash/Python script.")
    return raw


class Manager:
    def __init__(self, paths=None, files=None, runtime=None, downloads=None):
        self.paths = paths or Paths()
        self.files = files or SafeFiles()
        self.runtime = runtime or Runtime()
        self.runtime.paths = self.paths
        self.downloads = downloads or Downloads()

    def check_tls(self, config):
        if config.get("tls") is None:
            return
        p, f, r = self.paths, self.files, self.runtime
        identity = r.account(p.data)
        require(identity is not None, "固定服务账户不存在。")
        cert, key = tls_paths(config, p)
        for directory in (p.tls_root, cert.parent):
            f.check(directory, directory=True, mode=0o750, gid=identity[1])
        for path in (cert, key):
            f.check(path, mode=0o640, gid=identity[1])

    def imported_pem(self, path):
        p, f = Path(path), self.files
        require(p.is_absolute(), "导入证书必须使用绝对路径。")
        f.source_parents(p)
        entry = f.info(p)
        require(stat.S_ISREG(entry.st_mode) and not stat.S_ISLNK(entry.st_mode) and entry.st_nlink == 1
                and 0 < entry.st_size <= CERT_LIMIT, "导入证书必须是有界、非链接的常规 PEM 文件。")
        fd = os.open(p, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(fd, "rb") as stream:
            current = os.fstat(stream.fileno())
            require((current.st_dev, current.st_ino) == (entry.st_dev, entry.st_ino), "导入文件在读取期间变化。")
            raw = stream.read(CERT_LIMIT + 1)
        require(len(raw) == entry.st_size and len(raw) <= CERT_LIMIT, "导入文件大小发生变化。")
        return raw

    def prepare_tls(self, config, binary, cert_source=None, key_source=None):
        if not config["accessMode"].startswith("public-"):
            return config
        p, f, r = self.paths, self.files, self.runtime
        require(version_tuple(config["version"]) >= (0, 6, 0), "当前旧应用没有证书 CLI；先升级至 v0.6，再运行 qqon config 配置公网 HTTPS。")
        uid, gid = r.account(p.data)
        f.mkdir(p.tls_root, mode=0o750, gid=gid)
        generation = secrets.token_hex(16)
        directory = p.tls_root / generation
        require(not f.exists(directory), "新证书 generation 已存在；不会覆盖。")
        host = urllib.parse.urlsplit(config["origin"]).hostname
        if config["accessMode"] == "public-selfsigned":
            info = r.tls_info(binary, host, output=directory)
        else:
            require(cert_source is not None and key_source is not None and cert_source != key_source, "导入模式需要不同的证书和私钥来源。")
            cert_raw, key_raw = self.imported_pem(cert_source), self.imported_pem(key_source)
            f.mkdir(directory, mode=0o700)
            f.atomic_bytes(directory / "cert.pem", cert_raw, 0o600)
            f.atomic_bytes(directory / "key.pem", key_raw, 0o600)
            info = r.tls_info(binary, host, cert=directory / "cert.pem", key=directory / "key.pem")
        f.check(directory, directory=True, mode=0o700)
        for name in ("cert.pem", "key.pem"):
            path = directory / name
            f.check(path, mode=0o600)
            f.owner_mode(path, 0, gid, 0o640)
            fd = os.open(path, os.O_RDWR | getattr(os, "O_NOFOLLOW", 0))
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
        f.owner_mode(directory, 0, gid, 0o750)
        f.sync_dir(directory)
        f.sync_dir(p.tls_root)
        new = dict(config, tls={"generation": generation, "host": info["host"], "fingerprint": info["fingerprint"], "notAfter": info["notAfter"]})
        installation_config(config_bytes(new))
        self.check_tls(new)
        return new

    def inspect_management(self, config, check_unit=True):
        p, f, r = self.paths, self.files, self.runtime
        f.check(p.code_dir, directory=True, mode=0o755)
        f.check(p.config_dir, directory=True, mode=0o755)
        require(hashlib.sha256(f.read(p.installer, SCRIPT_LIMIT, mode=0o755)).hexdigest() == config["installerSHA256"],
                "Installed management script was modified; updates never silently replace it.")
        wrapper = f.read(p.updater, WRAPPER_LIMIT, mode=0o755)
        require(wrapper == WRAPPER, "Unknown or modified panel-update wrapper.")
        if config["installerSHA256"] == LEGACY_INSTALLER_SHA256:
            require(config["listen"] == LISTEN and hashlib.sha256(wrapper).hexdigest() == LEGACY_WRAPPER_SHA256,
                    "Unknown legacy manager settings or wrapper.")
            expected_unit = legacy_unit_bytes(config)
        else:
            expected_unit = unit_bytes(config)
        if config["format"] == 2:
            require(f.read(p.qqon, WRAPPER_LIMIT, mode=0o755) == QQON_WRAPPER, "qqon 快捷入口被修改，拒绝接管。")
            self.check_tls(config)
        if check_unit:
            require(f.read(p.unit, SUMS_LIMIT, mode=0o644) == expected_unit, "Unknown or modified service unit.")
        if f.exists(p.previous):
            f.check(p.previous, mode=0o755)
        identity = r.account(p.data)
        require(identity is not None, "The fixed service account is missing.")
        f.check(p.data, directory=True, mode=0o700, uid=identity[0], gid=identity[1])
        r.inspect_unit(p.unit)

    def inspect(self, arch):
        p, f, r = self.paths, self.files, self.runtime
        f.check(p.config_dir, directory=True, mode=0o755)
        raw = f.read(p.config, MANIFEST_LIMIT, mode=0o644)
        config = installation_config(raw)
        self.inspect_management(config)
        require(hashlib.sha256(f.read(p.binary, BINARY_LIMIT, mode=0o755)).hexdigest() == config["binarySHA256"],
                "Installed binary SHA-256 mismatch.")
        current = build_info(r.binary_info(p.binary), config["repository"], arch, config)
        return config, raw, current

    def status(self, url_only=False):
        # Read-only: no lock, recovery, migration, binary execution, secret reads or network.
        p, f, r = self.paths, self.files, self.runtime
        if not f.exists(p.config):
            print("未找到受支持的安装记录；只读查询未更改任何内容。")
            return
        config = installation_config(f.read(p.config, MANIFEST_LIMIT, mode=0o644))
        state = r.service_state()
        result = {"readOnly": True, "service": state, "running": state["ActiveState"] == "active",
                  "url": config["origin"] or "http://" + config["listen"], "externalReachability": "未检测"}
        if not url_only:
            result.update({key: config[key] for key in ("repository", "version", "listen", "origin", "dataSchema")})
            result["accessMode"] = config.get("accessMode", "proxy" if config["origin"] else "local")
            result["managerMatchesRecord"] = hashlib.sha256(f.read(p.installer, SCRIPT_LIMIT, mode=0o755)).hexdigest() == config["installerSHA256"]
            result["binaryMatchesRecord"] = hashlib.sha256(f.read(p.binary, BINARY_LIMIT, mode=0o755)).hexdigest() == config["binarySHA256"]
            result["pendingBinaryRecovery"] = f.exists(p.transaction)
            result["pendingManagerRecovery"] = f.exists(p.migration)
            result["pendingAccessRecovery"] = f.exists(p.access_transaction)
        if config.get("tls") is not None:
            result["tls"] = config["tls"]
            expiry = datetime.datetime.strptime(config["tls"]["notAfter"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.timezone.utc)
            result["certificateNotice"] = "证书已过期，请维护证书；不会忽略校验。" if expiry <= datetime.datetime.now(datetime.timezone.utc) else "到期前请续期并核对指纹。"
        print(json.dumps(result, ensure_ascii=False, sort_keys=True))

    def finish(self, config):
        address = config["origin"] or "http://" + config["listen"]
        print("已完成；实际配置地址：" + address)
        print("固定服务：" + UNIT + "；本机健康检查已通过；公网可达性：未检测。")
        if config.get("tls") is not None:
            print("证书 SHA-256 fingerprint：" + config["tls"]["fingerprint"] + "；到期时间：" + config["tls"]["notAfter"])
            if config["accessMode"] == "public-selfsigned":
                print("这是自签证书；浏览器会提示不受信任，请独立核对指纹。不会修改系统根信任。")
            else:
                print("已校验导入证书的配对、SAN 和有效期；不保证该证书受所有客户端信任。")
        print("初始化 token 文件路径：" + str(self.paths.data / "setup-token") + "（仅首次初始化时存在；从未输出内容）。")
        print("维护命令：sudo qqon；更新也可继续使用 sudo panel-update。")
        if config.get("accessMode", "").startswith("public-"):
            print("如需外部访问，请自行放行 TCP " + str(listener(config["listen"], public=True)) + "，并检查云安全组、NAT / 端口映射；未修改任何防火墙规则。")
        elif config["origin"]:
            print("本服务仅监听 " + config["listen"] + "；需已有反向代理，代理外部 TCP 端口 "
                  + str(urllib.parse.urlsplit(config["origin"]).port or 443) + " 的防火墙、云安全组及 NAT 请自行检查；未创建代理。")
        else:
            print("仅本机监听 " + config["listen"] + "；请使用本机或 SSH 隧道，不需要公网放行此端口。")

    def known_manager(self, source):
        p, f = self.paths, self.files
        if not f.exists(p.config):
            return
        config = installation_config(f.read(p.config, MANIFEST_LIMIT, mode=0o644))
        digest = hashlib.sha256(f.read(p.installer, SCRIPT_LIMIT, mode=0o755)).hexdigest()
        require(digest == config["installerSHA256"], "Installed management script was modified; refusing adoption.")
        require(digest in (hashlib.sha256(source).hexdigest(), *legacy_hashes()),
                "Unknown installed manager; only this reviewed manager or the pinned v0.4/v0.5 managers are supported.")

    def clear_marker(self, path, raw):
        p, f = self.paths, self.files
        require(path in (p.transaction, p.migration, p.access_transaction), "Unknown management marker path.")
        require(f.read(path, MANIFEST_LIMIT, mode=0o600) == raw, "Management transaction changed before completion.")
        try:
            os.unlink(path)
            f.sync_dir(path.parent)
        except BaseException:
            if not f.exists(path):
                f.atomic_bytes(path, raw, 0o600)
            raise

    def clear_transaction(self, raw):
        self.clear_marker(self.paths.transaction, raw)

    def manager_backup(self, old):
        return self.paths.manager_previous if old["installerSHA256"] == LEGACY_INSTALLER_SHA256 else self.paths.manager_v05_previous

    def recover_migration(self):
        p, f, r = self.paths, self.files, self.runtime
        if not f.exists(p.migration):
            return False
        require(not f.exists(p.transaction) and not f.exists(p.access_transaction), "Conflicting binary and manager transactions; refusing automatic recovery.")
        raw = f.read(p.migration, MANIFEST_LIMIT, mode=0o600)
        marker = manager_migration(raw)
        old, new = marker["old"], marker["new"]
        old_hash = old["installerSHA256"]
        backup_path = self.manager_backup(old)
        config = installation_config(f.read(p.config, MANIFEST_LIMIT, mode=0o644))
        require(config == old or (marker["phase"] == "ready" and config == new), "Manager migration does not match installed metadata.")
        current = f.read(p.installer, SCRIPT_LIMIT, mode=0o755)
        digest = hashlib.sha256(current).hexdigest()
        require(digest == old_hash or (marker["phase"] == "ready" and digest == new["installerSHA256"]),
                "Unknown manager or incomplete backup phase; refusing migration recovery.")
        backup = None
        if marker["phase"] == "ready" or f.exists(backup_path):
            backup = f.read(backup_path, SCRIPT_LIMIT, mode=0o755)
            require(hashlib.sha256(backup).hexdigest() == old_hash, "Manager backup SHA-256 mismatch.")
        if marker["format"] == 2 and f.exists(p.qqon):
            require(f.read(p.qqon, WRAPPER_LIMIT, mode=0o755) == QQON_WRAPPER, "迁移中的 qqon 入口被修改，拒绝恢复。")
        self.inspect_management(dict(old, installerSHA256=digest))
        require(hashlib.sha256(f.read(p.binary, BINARY_LIMIT, mode=0o755)).hexdigest() == old["binarySHA256"],
                "Installed binary SHA-256 mismatch during manager recovery.")
        try:
            with r.rollback_signals():
                if digest != old_hash:
                    f.atomic_bytes(p.installer, backup, 0o755)
                f.atomic_bytes(p.config, config_bytes(old), 0o644)
                if marker["format"] == 2 and f.exists(p.qqon):
                    os.unlink(p.qqon)
                    f.sync_dir(p.qqon.parent)
                self.clear_marker(p.migration, raw)
        except BaseException as exc:
            raise Failure("Manager migration recovery failed; journal and verified backup retained for retry.") from exc
        print("已恢复中断迁移前的官方旧管理器；应用、账户、访问方式、服务和 panel-update 均未更改。")
        return True

    def migrate_manager(self, old, source):
        p, f, r = self.paths, self.files, self.runtime
        require(not any(f.exists(path) for path in (p.transaction, p.migration, p.access_transaction)), "Pending transactions must be recovered before manager migration.")
        require(old["installerSHA256"] in legacy_hashes() and old["format"] == 1,
                "只能迁移固定摘要的官方 v0.4/v0.5 管理器。")
        require(not f.exists(p.qqon), "qqon 路径已存在，拒绝覆盖未知入口。")
        new = config2(old, hashlib.sha256(source).hexdigest())
        marker = {"format": 2, "phase": "prepared", "old": old, "new": new}
        raw = config_bytes(marker)
        manager_migration(raw)
        previous = f.read(p.installer, SCRIPT_LIMIT, mode=0o755)
        require(hashlib.sha256(previous).hexdigest() == old["installerSHA256"], "Legacy manager SHA-256 mismatch.")
        backup_path = self.manager_backup(old)
        if f.exists(backup_path):
            require(hashlib.sha256(f.read(backup_path, SCRIPT_LIMIT, mode=0o755)).hexdigest() == old["installerSHA256"],
                    "Unknown manager backup; refusing overwrite.")
        r.confirm("将迁移已核实的 v0.4/v0.5 管理器到本次审阅脚本，安装记录升级为 format2，生成 qqon。"
                  "旧二进制、账户数据、原 local/proxy 访问方式、服务 unit 和 panel-update 保持不变。必须单独终端确认。", False)
        f.atomic_bytes(p.migration, raw, 0o600)
        try:
            f.atomic_bytes(backup_path, previous, 0o755)
            require(hashlib.sha256(f.read(backup_path, SCRIPT_LIMIT, mode=0o755)).hexdigest() == old["installerSHA256"],
                    "Manager backup SHA-256 mismatch.")
            marker["phase"] = "ready"
            raw = config_bytes(marker)
            f.atomic_bytes(p.migration, raw, 0o600)
            f.atomic_bytes(p.installer, source, 0o755)
            require(hashlib.sha256(f.read(p.installer, SCRIPT_LIMIT, mode=0o755)).hexdigest() == new["installerSHA256"],
                    "New manager SHA-256 mismatch.")
            f.atomic_bytes(p.qqon, QQON_WRAPPER, 0o755)
            f.atomic_bytes(p.config, config_bytes(new), 0o644)
            self.clear_marker(p.migration, raw)
        except BaseException as exc:
            try:
                self.recover_migration()
            except BaseException as rollback:
                raise Failure("Manager migration failed; rollback also failed. Journal and backup retained; application data was not changed.") from rollback
            raise Failure("Manager migration failed; rollback succeeded. The pinned old manager was restored.") from exc
        print("管理器迁移完成，旧应用和原访问地址保留；请先 sudo qqon update 升级应用至 v0.6，再 sudo qqon config 配置公网 HTTPS。")

    def choose_access(self, options, old=None):
        r = self.runtime
        mode = options.mode
        if mode is None:
            if old is None:
                mode = "proxy" if options.origin else "public-selfsigned"
            else:
                require(not options.yes, "自动配置必须显式提供 --mode 和相应主机参数。")
                answer = r.prompt("访问方式：1 公网自签（默认） 2 本机 3 已有代理 4 导入证书 [1]：")
                require(answer in ("", "1", "2", "3", "4"), "访问方式选择无效。")
                mode = {"": "public-selfsigned", "1": "public-selfsigned", "2": "local", "3": "proxy", "4": "public-certificate"}[answer]
        port = options.port
        if old is not None and port is None and not options.yes:
            default = 8443 if mode.startswith("public-") else 5173
            reply = r.prompt("监听端口，1024–65535 [" + str(default) + "]：")
            port = listener("127.0.0.1:" + (reply or str(default)))
        host, proxy = options.host, options.origin
        if mode.startswith("public-") and host is None:
            require(not options.yes, "公网安装必须通过 --host 明确确认主机 IPv4 / 域名；不会查询外部 IP。")
            host = r.prompt("确认本服务器公网 IPv4（导入模式也可填证书域名；无外部 IP 查询）：")
        if mode == "proxy" and proxy is None:
            require(not options.yes, "proxy 模式必须提供 --origin 指向已有 HTTPS 代理。")
            proxy = r.prompt("已有 HTTPS 代理的外部地址：")
        settings = access_settings(mode, port, host, proxy)
        cert, key = options.cert, options.key
        if mode == "public-certificate":
            if not options.yes:
                cert = cert or r.prompt("证书 PEM 的绝对路径：")
                key = key or r.prompt("私钥 PEM 的绝对路径：")
            require(cert and key, "导入模式必须提供 --cert 和 --key。")
            for item in (cert, key):
                require(len(item) <= 4096 and Path(item).is_absolute() and not any(ord(c) < 32 or ord(c) == 127 for c in item), "证书来源路径无效。")
        else:
            require(cert is None and key is None, "只有导入模式接受 --cert 和 --key。")
        return settings, cert, key

    def recover_access(self, arch):
        p, f, r = self.paths, self.files, self.runtime
        if not f.exists(p.access_transaction):
            return False
        require(not f.exists(p.transaction) and not f.exists(p.migration), "存在冲突事务，拒绝自动恢复。")
        raw = f.read(p.access_transaction, MANIFEST_LIMIT, mode=0o600)
        marker = access_transaction(raw)
        old, new = marker["old"], marker["new"]
        current = installation_config(f.read(p.config, MANIFEST_LIMIT, mode=0o644))
        require(current in (old, new), "安装记录与访问事务不一致。")
        require(f.read(p.unit, SUMS_LIMIT, mode=0o644) in (unit_bytes(old), unit_bytes(new)), "服务 unit 与访问事务不一致。")
        self.inspect_management(old, check_unit=False)
        require(hashlib.sha256(f.read(p.binary, BINARY_LIMIT, mode=0o755)).hexdigest() == old["binarySHA256"], "恢复时应用摘要不匹配。")
        build_info(r.binary_info(p.binary), old["repository"], arch, old)
        try:
            with r.rollback_signals():
                r.stop_wait()
                f.atomic_bytes(p.unit, unit_bytes(old), 0o644)
                f.atomic_bytes(p.config, config_bytes(old), 0o644)
                r.systemctl("daemon-reload")
                if marker["wasRunning"]:
                    r.systemctl("start")
                    r.healthy(old)
                self.clear_marker(p.access_transaction, raw)
        except BaseException as exc:
            raise Failure("访问配置恢复失败；保留事务和证书 generation，需重试恢复；账户未更改。") from exc
        print("已恢复原访问配置和原服务运行状态；新旧证书 generation 均保留。")
        return True

    def apply_access(self, old, new, arch):
        p, f, r = self.paths, self.files, self.runtime
        require(not any(f.exists(path) for path in (p.transaction, p.migration, p.access_transaction)), "待恢复事务阻止配置变更。")
        marker = {"format": 1, "old": old, "new": new, "wasRunning": r.running()}
        raw = config_bytes(marker)
        access_transaction(raw)
        f.atomic_bytes(p.access_transaction, raw, 0o600)
        try:
            r.stop_wait()
            f.atomic_bytes(p.unit, unit_bytes(new), 0o644)
            f.atomic_bytes(p.config, config_bytes(new), 0o644)
            r.systemctl("daemon-reload")
            r.systemctl("start")
            r.healthy(new)
            if not marker["wasRunning"]:
                r.stop_wait()
            self.clear_marker(p.access_transaction, raw)
        except BaseException as exc:
            try:
                self.recover_access(arch)
            except BaseException as recovery:
                raise Failure("访问配置变更失败，恢复也失败；事务保留，未重置账户。") from recovery
            raise Failure("访问配置变更失败；已恢复旧配置和原运行状态，账户未更改。") from exc
        self.finish(new)
        if not marker["wasRunning"]:
            print("服务原本停止，检查后已恢复停止状态。")

    def backup(self, config):
        p, f, r = self.paths, self.files, self.runtime
        f.mkdir(p.backups, mode=0o700)
        require(p.data not in p.backups.parents and p.data != p.backups, "备份目录不能位于数据源内。")
        was_running = r.running()
        path = p.backups / ("backup-" + datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ-") + secrets.token_hex(8) + ".tar")
        total, count, budget = 0, 0, 10240
        failure = None
        try:
            r.stop_wait()
            with f.staging(p.backups) as stage:
                candidate = stage / "snapshot.tar"
                with open(candidate, "xb") as output:
                    f.owner_mode(candidate, 0, 0, 0o600)
                    with tarfile.open(fileobj=output, mode="w", format=tarfile.PAX_FORMAT) as archive:
                        def add(source, name):
                            nonlocal total, count, budget
                            entry = f.info(source)
                            require(not stat.S_ISLNK(entry.st_mode), "备份拒绝符号链接。")
                            require(stat.S_ISDIR(entry.st_mode) or (stat.S_ISREG(entry.st_mode) and entry.st_nlink == 1), "备份拒绝特殊文件和硬链接。")
                            count += 1
                            require(count <= BACKUP_FILES, "备份文件数超过 10000 上限。")
                            member = tarfile.TarInfo(name)
                            member.mtime, member.uid, member.gid = int(time.time()), 0, 0
                            member.mode = 0o700 if stat.S_ISDIR(entry.st_mode) else 0o600
                            if stat.S_ISDIR(entry.st_mode):
                                member.type = tarfile.DIRTYPE
                            else:
                                member.size = entry.st_size
                                total += entry.st_size
                            budget += 2048 + ((member.size + 511) // 512) * 512 + len(name.encode("utf-8"))
                            require(budget <= BACKUP_LIMIT and total <= BACKUP_LIMIT, "备份超过 512 MiB 上限。")
                            if member.isdir():
                                archive.addfile(member)
                                with os.scandir(source) as entries:
                                    for child in entries:
                                        if child.name in (".panel.lock", "lock") or child.name.endswith(".lock"):
                                            continue
                                        add(Path(child.path), name + "/" + child.name)
                            else:
                                fd = os.open(source, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
                                with os.fdopen(fd, "rb") as stream:
                                    opened = os.fstat(stream.fileno())
                                    require((opened.st_dev, opened.st_ino, opened.st_size) == (entry.st_dev, entry.st_ino, entry.st_size), "备份源在读取期间变化。")
                                    archive.addfile(member, stream)
                        add(p.data, "data")
                        for source, name in ((p.config, "metadata/install.json"), (p.unit, "metadata/server-panel.service"),
                                             (p.installer, "metadata/install.sh"), (p.updater, "metadata/panel-update"), (p.qqon, "metadata/qqon")):
                            add(source, name)
                        if config.get("tls") is not None:
                            cert, key = tls_paths(config, p)
                            add(cert, "tls/" + config["tls"]["generation"] + "/cert.pem")
                            add(key, "tls/" + config["tls"]["generation"] + "/key.pem")
                    output.flush()
                    os.fsync(output.fileno())
                require(f.info(candidate).st_size <= BACKUP_LIMIT, "备份归档超过 512 MiB 上限。")
                f.replace(candidate, path)
        except BaseException as exc:
            failure = exc
        finally:
            try:
                with r.rollback_signals():
                    if was_running:
                        r.systemctl("start")
                        r.healthy(config)
                    else:
                        r.stop_wait()
            except BaseException as recovery:
                raise Failure("备份" + ("失败" if failure else "已写入 " + str(path)) + "；原服务状态恢复失败，请手动检查 " + UNIT + "。账户未更改。") from recovery
        if failure is not None:
            raise Failure("备份失败（" + type(failure).__name__ + "）；已恢复原服务运行状态，未输出成功归档。") from failure
        print("一致性备份：" + str(path) + "；大小 " + str(f.info(path).st_size) + " 字节；已恢复原服务运行状态。")
        print("敏感备份含账户数据、管理记录及活动 TLS 私钥，仅 root 可读；不自动恢复或覆盖旧认证数据。")

    def manage(self, options, source, arch):
        action = options.manage
        if action in ("status", "url"):
            return self.status(url_only=action == "url")
        if action == "logs":
            return self.runtime.logs(options.lines)
        p, f, r = self.paths, self.files, self.runtime
        with r.lock(p, f):
            require(not f.exists(p.migration), "管理器迁移未完成；请使用审阅后的 install.sh --upgrade 恢复。")
            self.known_manager(source)
            if self.recover_transaction(arch) or self.recover_access(arch):
                return
            old, _, _ = self.inspect(arch)
            require(old["format"] == 2 and old["installerSHA256"] not in legacy_hashes(), "维护功能要求 format2；旧管理器必须单独确认迁移。")
            if action in ("start", "stop", "restart", "backup"):
                r.confirm("即将执行 " + {"start": "启动", "stop": "停止", "restart": "重启", "backup": "停服一致性备份"}[action] + "；仅操作固定服务 " + UNIT + "。", options.yes)
                if action == "backup":
                    return self.backup(old)
                if action == "stop":
                    r.stop_wait()
                else:
                    r.systemctl(action)
                    r.healthy(old)
                print("固定服务操作完成；实际状态：" + json.dumps(r.service_state(), ensure_ascii=False))
                return
            if action == "renew":
                require(old["accessMode"] == "public-selfsigned", "仅支持自签证书续期；导入证书请运行 qqon config 重新提供证书，本机 / 代理模式没有本机 TLS。")
                settings, cert, key = {k: old[k] for k in ACCESS_FIELDS}, None, None
            elif action == "config":
                settings, cert, key = self.choose_access(options, old)
            else:
                raise Failure("不支持该维护行为；未执行变更。")
            if settings["accessMode"].startswith("public-"):
                require(version_tuple(old["version"]) >= (0, 6, 0), "旧应用不支持证书 CLI；先 qqon update 升级 v0.6，再 qqon config。")
            r.confirm("确认" + ("续期自签证书" if action == "renew" else "调整访问配置") + "：" + (settings["origin"] or "http://" + settings["listen"])
                      + "；保留账户，新证书使用独立 generation；不修改防火墙或信任根。", options.yes)
            new = self.prepare_tls(dict(old, **settings), p.binary, cert, key)
            if new == old:
                print("配置未变化；未停止服务。")
                return
            self.apply_access(old, new, arch)

    def recover_transaction(self, arch):
        p, f, r = self.paths, self.files, self.runtime
        if not f.exists(p.transaction):
            return False
        f.check(p.config_dir, directory=True, mode=0o755)
        raw = f.read(p.transaction, MANIFEST_LIMIT, mode=0o600)
        transaction = update_transaction(raw)
        old, new = transaction["old"], transaction["new"]
        config = installation_config(f.read(p.config, MANIFEST_LIMIT, mode=0o644))
        require(config == old or (transaction["phase"] == "ready" and config == new),
                "Update transaction does not match installed metadata; refusing recovery.")
        self.inspect_management(old)
        current_digest = hashlib.sha256(f.read(p.binary, BINARY_LIMIT, mode=0o755)).hexdigest()
        if current_digest == old["binarySHA256"]:
            # prepared can be interrupted before panel.previous exists; current is still the verified old binary.
            source = p.binary
        else:
            require(transaction["phase"] == "ready" and current_digest == new["binarySHA256"],
                    "Unknown current binary or incomplete backup phase; refusing transaction recovery.")
            source = p.previous
            require(hashlib.sha256(f.read(source, BINARY_LIMIT, mode=0o755)).hexdigest() == old["binarySHA256"],
                    "Recovery previous binary SHA-256 mismatch.")
        # Never execute the new or an arbitrary marker-specified path during recovery.
        build_info(r.binary_info(source), old["repository"], arch, old)
        print("正在将中断的升级恢复至已校验的旧版本 v" + old["version"] + "。", flush=True)
        try:
            with r.rollback_signals():
                with f.staging(p.binary.parent) as directory:
                    restored = None
                    if source == p.previous:
                        restored = directory / "rollback"
                        f.copy_binary(source, restored, old["binarySHA256"])
                    # Stop may already have completed before its subprocess reported an error.
                    with contextlib.suppress(Failure):
                        r.systemctl("stop")
                    if restored is not None:
                        f.replace(restored, p.binary)
                    r.systemctl("start")
                    r.healthy(old)
                    f.atomic_bytes(p.config, config_bytes(old), 0o644)
                self.clear_transaction(raw)
        except BaseException as exc:
            raise Failure("Interrupted update recovery failed; the transaction marker and previous binary are retained for retry. "
                          "Account data was not reset. Cause: " + type(exc).__name__) from exc
        return True

    def initial_check(self):
        p, f, r = self.paths, self.files, self.runtime
        for path in (p.binary, p.previous, p.code_dir, p.updater, p.qqon, p.config_dir, p.unit, p.data):
            f.parents(path)
            require(not f.exists(path), "Unknown or partial installation exists; refusing adoption/overwrite: " + str(path))
        require(r.account(p.data) is None, "An unknown service account already exists.")
        r.inspect_unit(None)

    def target(self, repo, requested):
        suffix = "latest" if requested is None else "tags/" + requested
        url = "https://api.github.com/repos/" + repository(repo) + "/releases/" + suffix
        return release_api(self.downloads.get(url, API_LIMIT, 15, api=True), repo, requested)

    def stage_release(self, directory, repo, tag, sizes, arch):
        end = time.monotonic() + 120

        def fetch(name, limit, destination=None):
            remaining = end - time.monotonic()
            require(remaining > 0, "The total release transfer deadline expired.")
            result = self.downloads.get(canonical(repo, tag, name), limit, remaining, destination=destination)
            actual_size = len(result) if destination is None else result[0]
            require(actual_size == sizes[name], "Downloaded asset size differs from GitHub metadata.")
            return result

        sums = checksums(fetch("SHA256SUMS", SUMS_LIMIT))
        metadata = manifest(fetch("release.json", MANIFEST_LIMIT), sums, repo, tag)
        selected = next(binary for binary in metadata["binaries"] if binary["os"] == "linux" and binary["arch"] == arch)
        require(all(binary["archive"]["size"] == sizes[binary["archive"]["name"]] for binary in metadata["binaries"]),
                "Archive API/manifest size mismatch.")
        archive = selected["archive"]
        archive_path, candidate = directory / archive["name"], directory / "panel"
        size, archive_digest = fetch(archive["name"], archive["size"], archive_path)
        require(size == archive["size"] and archive_digest == archive["sha256"], "Downloaded archive SHA-256/size mismatch.")
        entry = self.files.check(archive_path, mode=0o600)
        fd = os.open(archive_path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(fd, "rb") as stream:
            opened = os.fstat(stream.fileno())
            require((opened.st_dev, opened.st_ino) == (entry.st_dev, entry.st_ino), "Staged archive changed while opening.")
            total, staged_hash = 0, hashlib.sha256()
            while True:
                chunk = stream.read(min(65536, archive["size"] + 1 - total))
                if not chunk:
                    break
                total += len(chunk)
                require(total <= archive["size"], "Staged archive size changed after download.")
                staged_hash.update(chunk)
            require(total == archive["size"] and staged_hash.hexdigest() == archive_digest, "Staged archive SHA-256/size changed after download.")
            # Verify the actual on-disk compressed bytes before attempting expansion.
            stream.seek(0)
            _, digest = extract_panel(stream, candidate, selected)
        self.files.owner_mode(candidate, 0, 0, 0o755)
        raw = self.files.read(candidate, BINARY_LIMIT, mode=0o755)
        require(len(raw) == selected["size"] and hashlib.sha256(raw).hexdigest() == digest, "Staged binary changed after extraction.")
        require(len(raw) >= 20 and raw[:6] == b"\x7fELF\x02\x01" and int.from_bytes(raw[18:20], "little") == {"amd64": 62, "arm64": 183}[arch],
                "The release binary is not the expected Linux ELF architecture.")
        build_info(self.runtime.binary_info(candidate), repo, arch, metadata)
        return candidate, metadata, digest

    def apply_update(self, directory, candidate, old, old_raw, new):
        p, f, r = self.paths, self.files, self.runtime
        require(not f.exists(p.transaction), "An unfinished update transaction must be recovered first.")
        transaction = {"format": 1, "phase": "prepared", "old": old, "new": new}
        raw = config_bytes(transaction)
        update_transaction(raw)
        # Durable intent before touching the backup. No downloaded path is stored in this marker.
        f.atomic_bytes(p.transaction, raw, 0o600)
        backup = directory / "previous"
        f.copy_binary(p.binary, backup, old["binarySHA256"])
        f.replace(backup, p.previous)
        transaction["phase"] = "ready"
        raw = config_bytes(transaction)
        # A durable ready marker means panel.previous was already fsynced before any stop/switch.
        f.atomic_bytes(p.transaction, raw, 0o600)
        try:
            r.systemctl("stop")
            f.replace(candidate, p.binary)
            r.systemctl("start")
            r.healthy(new)
            f.atomic_bytes(p.config, config_bytes(new), 0o644)
            self.clear_transaction(raw)
        except BaseException as update_error:
            with r.rollback_signals():
                try:
                    # A stop error does not prevent trying binary restoration and an explicit old-version probe.
                    with contextlib.suppress(Failure):
                        r.systemctl("stop")
                    restored = directory / "rollback"
                    f.copy_binary(p.previous, restored, old["binarySHA256"])
                    f.replace(restored, p.binary)
                    r.systemctl("start")
                    r.healthy(old)
                    f.atomic_bytes(p.config, old_raw, 0o644)
                    self.clear_transaction(raw)
                except BaseException as rollback_error:
                    raise Failure("Update failed; rollback also failed. Previous binary is preserved at " + str(p.previous)
                                  + "; the transaction marker is retained for the next invocation. Data was not reset. Cause: "
                                  + type(update_error).__name__ + "; rollback: " + type(rollback_error).__name__) from rollback_error
            raise Failure("Update failed; rollback succeeded and the previous version is healthy. Data was not reset. Cause: "
                          + type(update_error).__name__) from update_error

    def apply_install(self, candidate, config, source, cert_source=None, key_source=None):
        p, f, r = self.paths, self.files, self.runtime
        unit_written = False
        try:
            uid, gid = r.account(p.data, create=True)
            f.mkdir(p.code_dir)
            f.mkdir(p.config_dir)
            f.mkdir(p.data, mode=0o700, uid=uid, gid=gid)
            config = self.prepare_tls(config, candidate, cert_source, key_source)
            f.atomic_bytes(p.installer, source, 0o755)
            f.atomic_bytes(p.updater, WRAPPER, 0o755)
            f.atomic_bytes(p.qqon, QQON_WRAPPER, 0o755)
            f.atomic_bytes(p.unit, unit_bytes(config), 0o644)
            unit_written = True
            f.replace(candidate, p.binary)
            r.systemctl("daemon-reload")
            r.systemctl("start")
            r.healthy(config)
            r.systemctl("enable")
            f.atomic_bytes(p.config, config_bytes(config), 0o644)
            return config
        except BaseException as exc:
            stop_result = "service was not started"
            with r.rollback_signals():
                if unit_written:
                    try:
                        r.systemctl("stop")
                        r.systemctl("disable")
                        stop_result = "service stopped and disabled"
                    except BaseException:
                        stop_result = "could not confirm the service was stopped; manual inspection is required"
            raise Failure("Initial installation failed; " + stop_result
                          + ". Account/data and diagnostic files were retained; no successful installation was recorded."
                          + " Partial installations require administrator review, not automatic adoption.") from exc

    def execute(self, options, source, arch):
        p, f, r = self.paths, self.files, self.runtime
        if options.status:
            return self.status()
        if options.manage and options.manage != "update":
            return self.manage(options, source, arch)
        with r.lock(p, f):
            if self.recover_migration():
                return
            self.known_manager(source)
            if self.recover_access(arch):
                return
            recovered = self.recover_transaction(arch)
            if f.exists(p.config):
                old, old_raw, current = self.inspect(arch)
                if recovered:
                    print("中断的升级已恢复；v" + current["version"]
                          + " 健康检查通过。未迁移管理器或下载新版本；准备好后可重新运行本次审阅脚本。")
                    return
                if old["installerSHA256"] in legacy_hashes():
                    require(options.upgrade or options.migrate_manager,
                            "已知 v0.4/v0.5 管理器必须显式 --upgrade 或 --migrate-manager 并单独终端确认。")
                    self.migrate_manager(old, source)
                    return
                if options.migrate_manager:
                    print("当前已安装本次审阅的管理器，无需迁移。")
                    return
                if not (options.update or options.upgrade):
                    require(options.repo == old["repository"] and (options.origin is None or options.origin == old["origin"])
                            and (options.port is None or options.port == listener(old["listen"], public=old.get("accessMode", "").startswith("public-")))
                            and (options.mode is None or options.mode == old.get("accessMode"))
                            and options.host is None and options.cert is None and options.key is None,
                            "Existing installation uses different settings; it will not be overwritten.")
                    print("已安装 v" + current["version"] + "；账户、数据与配置保留。升级请使用 sudo qqon update 或 sudo panel-update。")
                    return
                repo = old["repository"]
            else:
                require(not (options.update or options.upgrade or options.migrate_manager),
                        "No recognized installation exists; first run the reviewed local install.sh.")
                self.initial_check()
                repo, old, old_raw = options.repo, None, None
            tag, sizes = self.target(repo, options.version)
            if old is not None:
                comparison = version_tuple(tag, tagged=True)
                require(comparison >= version_tuple(old["version"]), "Downgrades are unsupported; no service changes were made.")
                if comparison == version_tuple(old["version"]):
                    print("当前已运行 " + tag + "；未进行变更。")
                    return
            settings, cert_source, key_source = (self.choose_access(options) if old is None else ({k: old[k] for k in ACCESS_FIELDS if k in old}, None, None))
            if old is None and settings["accessMode"].startswith("public-"):
                require(version_tuple(tag, tagged=True) >= (0, 6, 0), "选择的旧应用没有证书 CLI；请安装 v0.6 或显式使用 --mode local / proxy。")
            action = "从 v" + old["version"] + " 升级" if old else "安装"
            r.confirm(action + "至 " + tag + "，仓库 " + repo + "；固定服务 " + UNIT + "；访问地址 "
                      + (settings["origin"] or "http://" + settings["listen"]) + "。\n仅信任所选 GitHub 仓库与 HTTPS；SHA-256 不是独立发布签名。", options.yes)
            with f.staging(p.binary.parent) as directory:
                candidate, metadata, digest = self.stage_release(directory, repo, tag, sizes, arch)
                config = dict(old) if old is not None else {"format": 2, "repository": repo, "dataSchema": 1,
                          "installerSHA256": hashlib.sha256(source).hexdigest(), **settings}
                config.update(version=metadata["version"], commit=metadata["commit"], buildTime=metadata["buildTime"], binarySHA256=digest)
                if old is None:
                    config = self.apply_install(candidate, config, source, cert_source, key_source)
                else:
                    self.apply_update(directory, candidate, old, old_raw, config)
                print("健康检查通过；账户、配置数据和 .panel.lock 均未删除。")
                self.finish(config)


def arguments(argv):
    class ChineseParser(argparse.ArgumentParser):
        def error(self, _message):
            self.exit(2, "命令行参数无效或缺少值；运行 --help 查看用法，不能覆盖服务或管理路径。\n")

    action = None
    if argv and argv[0] == "--manage":
        argv = argv[1:]
        action = argv[0] if argv and not argv[0].startswith("-") else "menu"
        if action != "menu" or (argv and argv[0] == "menu"):
            argv = argv[1:]
        if action == "tls":
            require(argv and argv[0] == "renew", "证书维护仅支持 qqon tls renew。")
            argv, action = argv[1:], "renew"
        require(action in ("menu", "status", "url", "start", "stop", "restart", "logs", "update", "backup", "config", "renew"), "不支持该维护命令。")
    parser = ChineseParser(description="QQON 中文安装与固定服务维护；公网 HTTPS 默认端口 8443。", allow_abbrev=False)
    parser._positionals.title = "位置参数"
    parser._optionals.title = "选项"
    parser.add_argument("--repo", help="公共 GitHub OWNER/REPO，默认 " + DEFAULT_REPO)
    parser.add_argument("--version", help="稳定版本标签，例如 v0.6.0，默认 latest")
    parser.add_argument("--mode", choices=ACCESS_MODES, help="访问模式，默认 public-selfsigned")
    parser.add_argument("--origin", help="proxy 模式已有的 HTTPS 代理地址")
    parser.add_argument("--port", help="端口 1024–65535，公网默认 8443，本机默认 5173")
    parser.add_argument("--host", help="经管理员确认的公网 IPv4；导入模式也支持域名")
    parser.add_argument("--cert", help="导入 PEM 证书的绝对路径，不保存来源路径")
    parser.add_argument("--key", help="导入 PEM 私钥的绝对路径，不输出内容")
    parser.add_argument("--lines", help=argparse.SUPPRESS)
    parser.add_argument("--yes", action="store_true", help="显式自动确认；不用于旧管理器迁移")
    parser.add_argument("--update", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--upgrade", action="store_true", help="升级应用或单独确认迁移已知旧管理器")
    parser.add_argument("--migrate-manager", action="store_true", help="仅迁移固定摘要的 v0.4/v0.5 管理器")
    parser.add_argument("--status", action="store_true", help="只读查询安装记录和实际服务状态")
    seen = set()
    for item in argv:
        if item.startswith("--"):
            name = item.split("=", 1)[0]
            require(name not in seen, "Duplicate command-line option.")
            seen.add(name)
    result = parser.parse_args(argv)
    result.manage = action
    if result.version is not None:
        version_tuple(result.version, tagged=True)
    if result.port is not None:
        result.port = listener("127.0.0.1:" + result.port)
    if result.origin is not None:
        result.origin = origin(result.origin, "127.0.0.1:" + str(result.port or 5173))[0]
    if result.host is not None:
        result.host = canonical_host(result.host, ipv4_only=result.mode in (None, "public-selfsigned"))
    settings = (result.repo, result.origin, result.port, result.mode, result.host, result.cert, result.key)
    actions = sum((result.update, result.upgrade, result.migrate_manager, result.status))
    require(actions <= 1 and not (action and actions), "Choose exactly one management action.")
    if action:
        allowed = {"logs": {"--lines"}, "update": {"--version", "--yes"},
                   "config": {"--mode", "--port", "--origin", "--host", "--cert", "--key", "--yes"}}
        flags = allowed.get(action, {"--yes"} if action in ("start", "stop", "restart", "backup", "renew") else set())
        require(seen <= flags, "该维护命令不接受这些参数；只读命令禁止变更选项。")
        result.update = action == "update"
    elif actions:
        require(all(value is None for value in settings), "Update/status settings come only from root-owned install.json; --repo/--origin/--port are forbidden.")
        require(not result.status or (result.version is None and not result.yes), "--status accepts no mutation options.")
    else:
        result.repo = repository(result.repo if result.repo is not None else DEFAULT_REPO)
    require(action == "logs" or result.lines is None, "--lines 仅用于 qqon logs。")
    if result.lines is not None:
        require(re.fullmatch(r"[1-9][0-9]{0,2}", result.lines, re.ASCII) and int(result.lines) <= 200, "日志行数范围为 1–200。")
    result.lines = int(result.lines or 50)
    return result


def main():
    options = arguments(sys.argv[2:])
    manager = Manager()
    if options.manage == "menu":
        answer = manager.runtime.prompt("QQON 维护\n1 状态  2 访问地址  3 启动  4 停止  5 重启\n6 日志  7 更新  8 备份  9 配置  10 自签证书续期  0 退出\n请选择 [0]：")
        choices = {"1": "status", "2": "url", "3": "start", "4": "stop", "5": "restart", "6": "logs",
                   "7": "update", "8": "backup", "9": "config", "10": "renew"}
        if answer in ("", "0"):
            return
        require(answer in choices, "菜单选择无效；未进行变更。")
        words = ["tls", "renew"] if answer == "10" else [choices[answer]]
        options = arguments(["--manage", *words])
    if options.status:
        manager.status()
        return
    if options.manage in ("status", "url", "logs"):
        manager.manage(options, None, None)
        return
    arch = manager.runtime.preflight(manager.files)
    source = read_source(sys.argv[1], manager.paths.installer, manager.files, options.update or bool(options.manage))
    os.umask(0o077)

    def interrupted(_signum, _frame):
        raise Interrupted("操作已中断；若已开始事务，将恢复原配置或应用。")

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    manager.execute(options, source, arch)


def user_error(error):
    message = str(error)
    if isinstance(error, (Failure, Interrupted)) and re.search(r"[\u3400-\u9fff]", message):
        return message
    translations = (
        ("Update failed; rollback also failed.", "E_UPDATE_RECOVERY", "升级失败且恢复失败；保留旧二进制和事务，需再次运行管理器恢复。账户数据未重置。"),
        ("Update failed; rollback succeeded", "E_UPDATE_RESTORED", "升级失败；已恢复上一版本并通过健康检查，账户数据未重置。"),
        ("Interrupted update recovery failed", "E_UPDATE_RECOVERY", "中断升级的恢复失败；旧二进制和事务均保留供重试，账户未重置。"),
        ("Initial installation failed;", "E_INSTALL_PARTIAL", "首次安装失败，未记录成功；已尝试停止和禁用服务。保留账户、数据及诊断文件，请检查实际服务状态后人工处理残留。"),
        ("Manager migration failed; rollback also failed", "E_MIGRATION_RECOVERY", "管理器迁移和恢复均失败；保留日志与已验证旧脚本，应用数据未更改。"),
        ("Manager migration failed; rollback succeeded", "E_MIGRATION_RESTORED", "管理器迁移失败；已恢复官方旧管理器，应用数据未更改。"),
        ("Manager migration recovery failed", "E_MIGRATION_RECOVERY", "管理器迁移恢复失败；保留事务与已验证备份，供再次恢复。"),
        ("Service did not become healthy", "E_HEALTH", "服务未在 30 秒内通过预期版本、schema 与 HTTP/HTTPS 健康检查；不会忽略证书校验。"),
        ("Health endpoint", "E_HEALTH", "本机健康端点未返回 HTTP 200。"),
        ("systemctl ", "E_SERVICE", "固定 systemd 服务操作失败；请用 qqon status 和 qqon logs 查看状态。"),
        ("Another installation/update", "E_BUSY", "另一安装或维护事务正在运行；请等待其完成。"),
        ("Installed management script was modified", "E_MANAGER_HASH", "已安装管理脚本与记录摘要不匹配；拒绝覆盖或接管，不能手改摘要绕过。"),
        ("Unknown installed manager", "E_MANAGER_UNKNOWN", "管理器身份未知；只支持本次审阅版本或固定摘要的官方 v0.4/v0.5 版本。"),
        ("Unknown or modified panel-update", "E_WRAPPER", "panel-update wrapper 不匹配官方固定字节；拒绝操作。"),
        ("Unknown or modified service unit", "E_UNIT", "固定服务 unit 被修改，拒绝接管。"),
        ("Unknown service unit overrides", "E_UNIT", "发现未知 systemd 覆盖配置，拒绝接管。"),
        ("No recognized installation", "E_NOT_INSTALLED", "未找到受支持的安装记录；请先运行审阅后的本机 install.sh。"),
        ("Existing installation uses different settings", "E_EXISTING_CONFIG", "现有安装使用不同设置，不会覆盖；请使用 qqon config 明确变更。"),
        ("Unknown or partial installation exists", "E_PARTIAL_INSTALL", "发现未知或未完成的安装；拒绝自动覆盖或接管，请管理员核查。"),
        ("Downgrades are unsupported", "E_DOWNGRADE", "不支持降级，未改变服务。"),
        ("Duplicate command-line option", "E_ARGUMENTS", "命令行参数重复。"),
        ("Choose exactly one management action", "E_ARGUMENTS", "一次只能选择一个管理操作。"),
        ("--status accepts no mutation", "E_READONLY", "--status 是只读操作，不接受变更参数。"),
        ("Update/status settings", "E_ARGUMENTS", "升级与状态参数只读取 root 所有的 install.json，不接受设置覆盖。"),
        ("Update mode may run only", "E_SOURCE", "升级 / 维护必须运行固定 root 所有的已安装脚本。"),
        ("Management requires root", "E_ROOT", "变更操作要求在运行 systemd 的 Linux 上以 root 执行。"),
        ("Only Linux amd64/arm64", "E_PLATFORM", "仅支持 Linux amd64/arm64。"),
        ("Python 3.8", "E_PYTHON", "依赖初始化后仍缺少可用的 Python 3.8+。"),
        ("A usable system CA", "E_CA", "系统 CA 证书库不可用，请修复依赖；不会忽略 HTTPS 校验。"),
        ("No supported system CA", "E_CA", "未找到支持的系统 CA 证书库；不会忽略 HTTPS 校验。"),
        ("GitHub HTTPS transfer failed", "E_DOWNLOAD_TLS", "GitHub HTTPS 下载失败；请检查连通性和系统 CA，未禁用 TLS 校验。"),
        ("Use GitHub owner/repo", "E_REPOSITORY", "请提供 GitHub OWNER/REPO，而不是 URL。"),
        ("Version must be", "E_VERSION", "版本号必须是稳定语义版本。"),
        ("Release versions must", "E_VERSION", "发行版本必须带 v 前缀。"),
        ("Only stable vMAJOR.MINOR.PATCH", "E_VERSION", "仅支持 v主版本.次版本.修订号 格式的稳定版本。"),
        ("Version component", "E_VERSION", "版本号分量超过允许上限。"),
        ("Pending transactions", "E_PENDING", "存在未完成的事务，必须先恢复后再迁移管理器。"),
        ("An unfinished update", "E_PENDING", "存在未完成的升级事务，必须先恢复。"),
        ("Conflicting binary", "E_TRANSACTION", "应用与管理器事务冲突，拒绝自动恢复。"),
        ("Manager migration may change only", "E_MIGRATION", "旧格式管理器迁移只允许 installerSHA256 改变，其他字段必须保持不变。"),
    )
    for prefix, code, text in translations:
        if message.startswith(prefix):
            return text + "（" + code + "）"
    groups = (
        (("SHA-256", "SHA256SUMS", "checksum", "digest"), "E_DIGEST", "文件摘要或校验清单不匹配，已拒绝继续；不允许改摘要绕过。"),
        (("origin", "Origin", "unit arguments"), "E_ADDRESS", "访问地址格式或 origin / healthHost 一致性校验失败。"),
        (("transaction", "migration", "marker", "backup phase"), "E_TRANSACTION", "事务格式、固定字段或恢复阶段校验失败；保留原数据，拒绝不受支持的恢复。"),
        (("tar", "gzip", "archive", "Archive", "USTAR", "ELF"), "E_ARCHIVE", "发行归档未通过格式、大小或安全成员校验，只允许预期的 Linux 二进制。"),
        (("build-info", "binary", "Binary", "commit", "Build time", "build time", "Staged"), "E_BINARY_IDENTITY", "应用身份、平台、构建信息或文件内容未通过校验。"),
        (("GitHub", "release", "Release", "asset", "manifest", "Manifest", "format 3"), "E_RELEASE", "GitHub 发行信息、format 3 九资产结构或来源校验失败。"),
        (("JSON", "metadata", "schema", "installation format"), "E_METADATA", "安装 / 发行记录不是受支持的严格 JSON 格式，或字段 / schema 不一致。"),
        (("account", "group", "privileges"), "E_ACCOUNT", "固定服务账户或组校验 / 创建失败；拒绝接管未知或过权账户。"),
        (("unit", "service"), "E_UNIT", "固定服务 unit 状态或路径校验失败，未接管未知服务。"),
        (("time limit", "timed out", "deadline"), "E_TIMEOUT", "操作超过允许的时间限制，已终止。"),
        (("size limit", "output limit", "limit exceeded"), "E_LIMIT", "管理文件或子进程输出超过安全大小限制。"),
        (("Download", "download", "redirect", "HTTPS", "transfer"), "E_DOWNLOAD", "下载来源、重定向、内容大小或完整性校验失败；未忽略 HTTPS 验证。"),
        (("owner", "permission", "path", "source", "link", "file", "filesystem", "destination", "Source"), "E_PATH", "管理路径、文件类型、所有者、权限或原子替换条件不满足，已拒绝操作。"),
    )
    for words, code, text in groups:
        if any(word in message for word in words):
            return text + "（" + code + "）"
    return "操作未完成；未尝试不安全的回退，请检查配置、磁盘空间及服务状态。（E_" + type(error).__name__ + "）"


if __name__ == "__main__":
    try:
        main()
    except (Failure, Interrupted, OSError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        print("QQON 安装器：" + user_error(error), file=sys.stderr)
        sys.exit(1)
PANEL_PYTHON
