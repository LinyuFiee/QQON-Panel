#!/bin/bash
# QQON-Panel one-command entry. Pipeline input is never used for confirmation.
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH LANG=C LC_ALL=C
unset BASH_ENV ENV PYTHONPATH PYTHONHOME CURL_HOME CURL_CA_BUNDLE SSL_CERT_FILE SSL_CERT_DIR

fail() { printf 'QQON-Panel: %s\n' "$*" >&2; exit 1; }
if [[ ${1-} == --help || ${1-} == -h ]]; then
    printf '%s\n' 'QQON-Panel: run this entry as root to open the interactive installer.' \
        'Downloads only the current official Release; v0.5.0 or later is required.' \
        'For an offline/read-only status check, run the already installed local manager.'
    exit 0
fi
(( EUID == 0 )) || fail '请在 root 终端运行；本次未安装任何软件。'
[[ -r /proc/sys/kernel/ostype ]] || fail '此入口仅用于 Linux；Windows 请使用 PowerShell 入口。'
IFS= read -r kernel < /proc/sys/kernel/ostype
[[ $kernel == Linux ]] || fail '此入口仅用于 Linux。'
for tool in stat mktemp chmod wc rm; do command -v "$tool" >/dev/null || fail "缺少基础工具 $tool"; done
[[ -d /root && ! -L /root && $(stat -c %u /root) == 0 ]] || fail '/root 必须是 root 所有的真实目录。'
root_mode=$(stat -c %a /root)
(( (8#$root_mode & 0022) == 0 )) || fail '/root 不能允许其他用户写入。'
if command -v curl >/dev/null; then downloader=curl
elif command -v wget >/dev/null; then downloader=wget
else fail '需要 curl 或 wget 启动网络安装。也可下载官方 install.sh 后上传到服务器执行。'
fi
command -v sha256sum >/dev/null || fail '缺少 sha256sum；请安装发行版 coreutils 后重试。'
umask 077
stage=$(mktemp -d /root/.qqon-bootstrap.XXXXXXXX)
cleanup() { rm -f -- "$stage/install.sh" "$stage/SHA256SUMS" "$stage/headers"; rmdir -- "$stage" 2>/dev/null || true; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

allowed_url() {
    local url=$1 host path
    [[ $url == https://* && ${#url} -le 8192 && $url != *$'\n'* && $url != *$'\r'* && $url != *' '* && $url != *'\\'* ]] || return 1
    host=${url#https://}; host=${host%%/*}
    case $host in
        github.com)
            path=${url#https://github.com}
            [[ $path == /LinyuFiee/QQON-Panel/releases/latest/download/* || $path == /LinyuFiee/QQON-Panel/releases/download/v*/* ]] ;;
        release-assets.githubusercontent.com|objects.githubusercontent.com|github-releases.githubusercontent.com) return 0 ;;
        *) return 1 ;;
    esac
}

fetch() {
    local address=$1 output=$2 maximum=$3 step status location line result
    for step in 0 1 2 3 4 5; do
        allowed_url "$address" || fail '下载重定向离开了官方 GitHub 资源主机。'
        : > "$stage/headers"
        if [[ $downloader == curl ]]; then
            status=$( (ulimit -f $(((maximum + 1023) / 1024)); curl -q --silent --show-error --proto '=https' --connect-timeout 10 --max-time 90 \
                --max-filesize "$maximum" --max-redirs 0 --dump-header "$stage/headers" \
                --output "$output" --write-out '%{http_code}' "$address") ) || fail '下载失败，请检查 GitHub 连通性和系统 CA 证书；不会跳过 HTTPS 校验。'
        else
            # Bound file output as well as time. wget stops at redirects; inspect them below.
            result=0
            (ulimit -f $(((maximum + 1023) / 1024)); wget --no-config --https-only --timeout=30 --tries=1 \
                --max-redirect=0 --server-response --output-document="$output" "$address") 2> "$stage/headers" || result=$?
            status=''
            while IFS= read -r line; do
                if [[ $line =~ HTTP/[0-9.]+[[:space:]]+([0-9]{3}) ]]; then status=${BASH_REMATCH[1]}; fi
            done < "$stage/headers"
            [[ -n $status ]] || fail 'wget 下载失败，未获得有效 HTTPS 响应。'
        fi
        [[ $(wc -c < "$output") -le $maximum ]] || fail '下载文件超过安全大小限制。'
        case $status in
            200) return 0 ;;
            301|302|303|307|308)
                location=''
                while IFS= read -r line; do
                    line=${line%$'\r'}
                    if [[ $line =~ ^[[:space:]]*[Ll][Oo][Cc][Aa][Tt][Ii][Oo][Nn]:[[:space:]]*(.*)$ ]]; then
                        location=${BASH_REMATCH[1]}
                        location=${location%" [following]"}
                    fi
                done < "$stage/headers"
                [[ -n $location ]] || fail '重定向没有合法地址。'
                address=$location ;;
            404) fail '官方 Release 尚未提供交互安装器，请等待 v0.5.0 发布或查看文档的 v0.4.0 安装页。' ;;
            *) fail "GitHub 返回 HTTP $status，安装未执行。" ;;
        esac
    done
    fail '下载重定向次数过多。'
}

printf '%s\n' 'QQON-Panel：从 LinyuFiee/QQON-Panel 下载官方安装器，随后在终端交互。'
fetch 'https://github.com/LinyuFiee/QQON-Panel/releases/latest/download/SHA256SUMS' "$stage/SHA256SUMS" 16384
fetch 'https://github.com/LinyuFiee/QQON-Panel/releases/latest/download/install.sh' "$stage/install.sh" 524288
expected='' count=0
while IFS= read -r line || [[ -n $line ]]; do
    if [[ $line =~ ^([0-9a-f]{64})[[:space:]][[:space:]]install\.sh$ ]]; then
        expected=${BASH_REMATCH[1]}; count=$((count + 1))
    fi
done < "$stage/SHA256SUMS"
[[ $count == 1 ]] || fail '校验清单缺少唯一的 install.sh。'
actual=$(sha256sum "$stage/install.sh"); actual=${actual%% *}
[[ $actual == "$expected" ]] || fail '安装器摘要不匹配，未执行任何下载代码。'
{ IFS= read -r first; IFS= read -r second; } < "$stage/install.sh"
[[ $first == '#!/bin/bash' && $second == '# QQON-Panel interactive installer protocol: 3' ]] ||
    fail '当前稳定版仍为旧安装器；一键交互入口需要 v0.5.0，请查看文档中的旧版安装方式。'
chmod 700 "$stage/install.sh"
printf '%s\n' '摘要校验完成。此校验信任官方仓库，不等同独立发布签名。'
/bin/bash -p "$stage/install.sh" "$@"
