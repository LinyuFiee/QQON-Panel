<#
.SYNOPSIS
QQON-Panel Windows 安装与维护（原生 64 位 PowerShell 5.1）。
.DESCRIPTION
默认引导公网 IPv4、自签 HTTPS、端口 8443；不修改防火墙或系统证书信任。
维护入口：qqon；qqon status/url/start/stop/restart/logs/update/backup/config；qqon tls renew。
-Yes 不授予系统管理权限；最高权限另需 -AcceptSystemManagement 或专门中文确认。
PATH 追加另需 -AddToPath；旧版管理器迁移另需 -MigrateLegacy。
#>
[CmdletBinding()]
param(
    [string]$Action,
    [int]$Port = 8443,
    [string]$Origin = '',
    [string]$Version = '',
    [switch]$Yes,
    [switch]$AcceptSystemManagement,
    [string]$AccessMode = 'public-selfsigned',
    [string]$PublicHost = '',
    [string]$Certificate = '',
    [string]$PrivateKey = '',
    [switch]$AddToPath,
    [switch]$MigrateLegacy,
    [string]$Maintenance = '',
    [string[]]$MaintenanceArguments = @()
)

# BEGIN SHARED RELEASE READER (kept byte-identical to bootstrap.ps1 by tests)
function Assert-QP {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw (Get-QPFailureText $Message) }
}

function Get-QPFailureText {
    param([string]$Message)
    if ($Message -match '[\u3400-\u9fff]') { return $Message }
    switch -Regex ($Message) {
        'JSON|metadata fields|metadata field' { return 'JSON 格式、字段、类型、编码或长度不符合约定；拒绝重复或额外字段。[QP_JSON]' }
        'nine|eight|asset.*count|asset.*set|format 3|format 2|v0.5.0|binary record count' { return '发行包必须是 v0.5.0 或以上的 format3 九资产集合，manifest 和摘要记录必须精确匹配。[QP_RELEASE_ASSETS]' }
        'Version|version|stable|Downgrade' { return '版本或程序身份不符合约定；只接受规范的稳定版本，不允许降级或同版本换包。[QP_VERSION]' }
        'SHA-256|digest|checksum|hash|modified|CRC' { return '文件的 SHA-256、校验记录、CRC 或已保存身份不一致，已拒绝使用该文件。[QP_INTEGRITY]' }
        'URL|HTTPS|redirect|TLS certificate|GitHub|Download' { return 'HTTPS 下载、地址或响应校验失败；仅允许固定 GitHub 仓库及 CDN，不跳过证书验证。[QP_HTTPS]' }
        'size|limit|empty|deadline|Truncated|timed-out' { return '文件、响应或操作超出大小/时间限制，或读取不完整，已停止操作。[QP_LIMIT]' }
        'Build-info|build-info|Build time|calendar|commit|Repository|schema|identity|platform' { return '程序、仓库、提交、平台或数据格式身份不符合约定；不会迁移或重置账户。[QP_IDENTITY]' }
        'JSON' { return 'JSON 数据不符合严格格式。[QP_JSON]' }
        'ZIP' { return 'Windows ZIP 格式不安全；只允许单个普通 panel.exe，拒绝目录、链接、额外字段或压缩边界异常。[QP_ZIP]' }
        'PE|AMD64|MZ|DLL|subsystem' { return '程序必须是有效 AMD64 Windows 控制台 PE 文件，拒绝 32 位程序或 DLL。[QP_PE]' }
        'ACL|owner|access|inherit|private|unprivileged|parent' { return '路径所有者或受保护 ACL 不符合私有权限约定；拒绝宽泛、可写或不安全的继承权限。[QP_ACL]' }
        'PowerShell|Administrator|amd64 Windows|Windows 10/11|NTFS' { return '请在受支持的原生 amd64 Windows 上使用管理员 64 位 PowerShell 5.1；安装目录必须位于固定 NTFS 卷。[QP_HOST]' }
        'path|Path|file|directory|reparse|symlink|component' { return '文件或目录路径不符合约定；必须是规范本地路径，拒绝链接、重解析点、未知现有文件或缺失文件。[QP_PATH]' }
        'mutex|Another QQON' { return '安装或维护正在进行，或全局互斥锁权限不安全；未并行启动另一事务。[QP_LOCK]' }
        'service|Service|SCM|process|Process' { return '固定 QQONPanel 服务身份、SCM 配置、状态或进程检查未通过；未强制结束进程或绕过文件锁。[QP_SERVICE]' }
        'Port|port|Origin|origin|DNS|domain|Listener|loopback' { return '监听端口或已有 HTTPS 代理地址不合法；请检查规范端口、域名和访问模式。[QP_ACCESS]' }
        'argument|Argument' { return '进程参数无效，拒绝执行。[QP_ARGUMENT]' }
        default { return '系统操作未完成；请检查权限、磁盘空间、网络或保留的事务状态后重试。未显示可能含敏感信息的底层异常。[QP_SYSTEM]' }
    }
}

function Get-QPAssetLimits {
    param([switch]$Legacy)
    $limits = @{
        'panel-linux-amd64.tar.gz' = 67108864; 'panel-linux-arm64.tar.gz' = 67108864
        'install.sh' = 524288; 'update.sh' = 32768; 'release.json' = 32768; 'SHA256SUMS' = 16384
    }
    if (-not $Legacy) {
        $limits['panel-windows-amd64.zip'] = 67108864
        $limits['install.ps1'] = 524288
        $limits['update.ps1'] = 32768
    }
    return $limits
}

function Read-QPJsonString {
    param([hashtable]$State)
    $text = $State.Text
    Assert-QP ($State.Pos -lt $text.Length -and $text[$State.Pos] -ceq '"') 'Expected JSON string.'
    $State.Pos++
    $builder = New-Object System.Text.StringBuilder
    $closed = $false
    while ($State.Pos -lt $text.Length) {
        $ch = $text[$State.Pos]; $State.Pos++
        if ($ch -ceq '"') { $closed = $true; break }
        Assert-QP ([int]$ch -ge 32) 'Control character in JSON string.'
        if ($ch -ceq '\') {
            Assert-QP ($State.Pos -lt $text.Length) 'Truncated JSON escape.'
            $ch = $text[$State.Pos]; $State.Pos++
            switch -CaseSensitive ($ch) {
                '"' { [void]$builder.Append('"') }
                '\' { [void]$builder.Append('\') }
                '/' { [void]$builder.Append('/') }
                'b' { [void]$builder.Append([char]8) }
                'f' { [void]$builder.Append([char]12) }
                'n' { [void]$builder.Append([char]10) }
                'r' { [void]$builder.Append([char]13) }
                't' { [void]$builder.Append([char]9) }
                'u' {
                    Assert-QP ($State.Pos + 4 -le $text.Length) 'Truncated JSON Unicode escape.'
                    $hex = $text.Substring($State.Pos, 4)
                    Assert-QP ($hex -cmatch '\A[0-9a-fA-F]{4}\z') 'Invalid JSON Unicode escape.'
                    [void]$builder.Append([char][Convert]::ToUInt16($hex, 16)); $State.Pos += 4
                }
                default { throw 'Invalid JSON escape.' }
            }
        } else { [void]$builder.Append($ch) }
    }
    Assert-QP $closed 'Unterminated JSON string.'
    $value = $builder.ToString()
    for ($i = 0; $i -lt $value.Length; $i++) {
        if ([char]::IsHighSurrogate($value[$i])) {
            Assert-QP ($i + 1 -lt $value.Length -and [char]::IsLowSurrogate($value[$i + 1])) 'Unpaired JSON surrogate.'
            $i++
        } else { Assert-QP (-not [char]::IsLowSurrogate($value[$i])) 'Unpaired JSON surrogate.' }
    }
    return $value
}

function Skip-QPJsonSpace {
    param([hashtable]$State)
    while ($State.Pos -lt $State.Text.Length -and [int]$State.Text[$State.Pos] -in @(9, 10, 13, 32)) { $State.Pos++ }
}

function Read-QPJsonValue {
    param([hashtable]$State, [int]$Depth = 0)
    Assert-QP ($Depth -le 8) 'JSON nesting exceeds eight levels.'
    Skip-QPJsonSpace $State
    Assert-QP ($State.Pos -lt $State.Text.Length) 'Truncated JSON.'
    $ch = $State.Text[$State.Pos]
    if ($ch -ceq '"') { return Read-QPJsonString $State }
    if ($ch -ceq '{') {
        $State.Pos++; Skip-QPJsonSpace $State
        $map = [hashtable]::new([StringComparer]::Ordinal)
        $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
        if ($State.Pos -lt $State.Text.Length -and $State.Text[$State.Pos] -ceq '}') { $State.Pos++; return ,$map }
        while ($true) {
            Skip-QPJsonSpace $State
            $key = Read-QPJsonString $State
            Assert-QP ($seen.Add($key)) 'Duplicate JSON field (including case variants).'
            Skip-QPJsonSpace $State
            Assert-QP ($State.Pos -lt $State.Text.Length -and $State.Text[$State.Pos] -ceq ':') 'Missing JSON colon.'
            $State.Pos++
            $map[$key] = Read-QPJsonValue $State ($Depth + 1)
            Skip-QPJsonSpace $State
            Assert-QP ($State.Pos -lt $State.Text.Length) 'Truncated JSON object.'
            $next = $State.Text[$State.Pos]; $State.Pos++
            if ($next -ceq '}') { return ,$map }
            Assert-QP ($next -ceq ',') 'Missing JSON object separator.'
        }
    }
    if ($ch -ceq '[') {
        $State.Pos++; Skip-QPJsonSpace $State
        $items = [Collections.Generic.List[object]]::new()
        if ($State.Pos -lt $State.Text.Length -and $State.Text[$State.Pos] -ceq ']') { $State.Pos++; return ,($items.ToArray()) }
        while ($true) {
            [void]$items.Add((Read-QPJsonValue $State ($Depth + 1)))
            Skip-QPJsonSpace $State
            Assert-QP ($State.Pos -lt $State.Text.Length) 'Truncated JSON array.'
            $next = $State.Text[$State.Pos]; $State.Pos++
            if ($next -ceq ']') { return ,($items.ToArray()) }
            Assert-QP ($next -ceq ',') 'Missing JSON array separator.'
        }
    }
    foreach ($literal in @('true', 'false', 'null')) {
        if ($State.Text.Substring($State.Pos).StartsWith($literal, [StringComparison]::Ordinal)) {
            $State.Pos += $literal.Length
            if ($literal -ceq 'true') { return $true }
            if ($literal -ceq 'false') { return $false }
            return ,$null
        }
    }
    $match = [regex]::Match($State.Text.Substring($State.Pos), '\A-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?')
    Assert-QP $match.Success 'Invalid JSON value.'
    $State.Pos += $match.Length
    [long]$integer = 0
    if ($match.Value -cnotmatch '[.eE]' -and [long]::TryParse($match.Value, [Globalization.NumberStyles]::AllowLeadingSign, [Globalization.CultureInfo]::InvariantCulture, [ref]$integer)) { return $integer }
    [double]$number = 0
    Assert-QP ([double]::TryParse($match.Value, [Globalization.NumberStyles]::Float, [Globalization.CultureInfo]::InvariantCulture, [ref]$number) -and -not [double]::IsInfinity($number) -and -not [double]::IsNaN($number)) 'Invalid or non-finite JSON number.'
    return $number
}

function ConvertFrom-QPJson {
    param([byte[]]$Bytes, [int]$Limit = 32768)
    Assert-QP ($null -ne $Bytes -and $Bytes.Length -gt 0 -and $Bytes.Length -le $Limit) 'JSON exceeds its byte limit or is empty.'
    $utf8 = [Text.UTF8Encoding]::new($false, $true)
    $state = @{ Text = $utf8.GetString($Bytes); Pos = 0 }
    $value = Read-QPJsonValue $state
    Skip-QPJsonSpace $state
    Assert-QP ($state.Pos -eq $state.Text.Length -and $value -is [hashtable]) 'Expected exactly one UTF-8 JSON object (no BOM/trailing data).'
    return ,$value
}

function Assert-QPFields {
    param($Value, [string[]]$Fields)
    Assert-QP ($Value -is [hashtable] -and $Value.Count -eq $Fields.Count) 'Missing or unexpected metadata fields.'
    foreach ($field in $Fields) { Assert-QP ($Value.ContainsKey($field)) 'Missing or incorrectly cased metadata field.' }
}

function Test-QPInteger {
    param($Value, [long]$Min, [long]$Max)
    return (($Value -is [int] -or $Value -is [long]) -and $Value -ge $Min -and $Value -le $Max)
}

function Get-QPSha256 {
    param([byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($sha.ComputeHash($Bytes))).Replace('-', '').ToLowerInvariant() } finally { $sha.Dispose() }
}

function ConvertTo-QPVersion {
    param($Value, [switch]$Tagged)
    Assert-QP ($Value -is [string]) 'Version must be a string.'
    $pattern = '\A(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\z'
    if ($Tagged) { $pattern = '\Av' + $pattern.Substring(2) }
    $m = [regex]::Match($Value, $pattern)
    Assert-QP $m.Success 'Only stable vMAJOR.MINOR.PATCH releases without leading zeroes are supported.'
    for ($i = 1; $i -le 3; $i++) { Assert-QP ([long]$m.Groups[$i].Value -le 2147483647) 'Version component is too large.' }
    return [version]::new([int]$m.Groups[1].Value, [int]$m.Groups[2].Value, [int]$m.Groups[3].Value)
}

function Assert-QPIdentity {
    param($Value)
    [void](ConvertTo-QPVersion $Value['version'])
    Assert-QP ($Value['repository'] -is [string] -and $Value['repository'] -ceq 'LinyuFiee/QQON-Panel') 'Repository is fixed to LinyuFiee/QQON-Panel.'
    Assert-QP (Test-QPInteger $Value['dataSchema'] 1 1) 'Only data schema 1 is supported; automatic data migration is forbidden.'
    Assert-QP ($Value['commit'] -is [string] -and $Value['commit'] -cmatch '\A(?:[0-9a-f]{40}|[0-9a-f]{64})\z') 'Invalid release commit.'
    Assert-QP ($Value['buildTime'] -is [string] -and $Value['buildTime'] -cmatch '\A[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\z') 'Build time must be UTC seconds.'
    $parsed = [datetime]::MinValue
    Assert-QP ([datetime]::TryParseExact($Value['buildTime'], "yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::None, [ref]$parsed)) 'Invalid release calendar date.'
}

function Get-QPAssetUrl {
    param([string]$Tag, [string]$Name)
    [void](ConvertTo-QPVersion $Tag -Tagged)
    Assert-QP ((Get-QPAssetLimits).ContainsKey($Name)) 'Unknown release asset.'
    return "https://github.com/LinyuFiee/QQON-Panel/releases/download/$Tag/$Name"
}

function Assert-QPDownloadUrl {
    param([string]$Url, [string]$Initial, [switch]$Api)
    Assert-QP ($Url.Length -le 8192 -and $Url -cmatch '\A[\x21-\x7e]+\z' -and -not $Url.Contains('\') -and -not $Url.Contains('#')) 'Invalid HTTPS URL.'
    $uri = $null
    Assert-QP ([uri]::TryCreate($Url, [UriKind]::Absolute, [ref]$uri)) 'Invalid absolute URL.'
    Assert-QP ($uri.Scheme -ceq 'https' -and $uri.Port -eq 443 -and $uri.UserInfo.Length -eq 0 -and $uri.Fragment.Length -eq 0) 'Only verified HTTPS without credentials or fragments is allowed.'
    if ($Api) {
        Assert-QP ($Url -ceq $Initial -and $uri.Host -ceq 'api.github.com' -and $Url -cmatch '\Ahttps://api\.github\.com/repos/LinyuFiee/QQON-Panel/releases/(?:latest|tags/v[0-9]+\.[0-9]+\.[0-9]+)\z') 'API redirects or non-canonical repositories are forbidden.'
    } else {
        Assert-QP ($uri.Host -cin @('github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com', 'github-releases.githubusercontent.com')) 'Download is outside the fixed GitHub CDN allowlist.'
        if ($uri.Host -ceq 'github.com') {
            Assert-QP ($Url -ceq $Initial -and $Url -cmatch '\Ahttps://github\.com/LinyuFiee/QQON-Panel/releases/download/v[0-9]+\.[0-9]+\.[0-9]+/[A-Za-z0-9_.-]+\z') 'Asset path is not canonical.'
        }
    }
}

function Read-QPDownload {
    param([string]$Url, [int]$Limit, [switch]$Api)
    Assert-QP ($Limit -gt 0 -and $Limit -le 67108864) 'Invalid download limit.'
    Assert-QP ($null -eq [Net.ServicePointManager]::ServerCertificateValidationCallback) 'An overridden TLS certificate validator is not supported.'
    Assert-QPDownloadUrl $Url $Url -Api:$Api
    $oldTls = [Net.ServicePointManager]::SecurityProtocol
    $clock = [Diagnostics.Stopwatch]::StartNew()
    $deadline = 120000; if ($Api) { $deadline = 15000 }
    $current = $Url
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        for ($redirect = 0; $redirect -le 5; $redirect++) {
            Assert-QPDownloadUrl $current $Url -Api:$Api
            $remaining = $deadline - [int]$clock.ElapsedMilliseconds
            Assert-QP ($remaining -gt 0) 'HTTPS download deadline expired.'
            $request = [Net.HttpWebRequest]::CreateHttp($current)
            $request.AllowAutoRedirect = $false; $request.Proxy = $null
            $request.Timeout = [Math]::Min(15000, $remaining); $request.ReadWriteTimeout = $request.Timeout
            $request.UserAgent = 'QQON-Panel-Windows-Installer/3'
            $request.Accept = 'application/octet-stream'; if ($Api) { $request.Accept = 'application/vnd.github+json' }
            $request.Headers['Accept-Encoding'] = 'identity'
            $request.AutomaticDecompression = [Net.DecompressionMethods]::None
            $response = $null
            try {
                $response = $request.GetResponse()
                $code = [int]$response.StatusCode
                if ($code -in @(301, 302, 303, 307, 308)) {
                    Assert-QP (-not $Api -and $redirect -lt 5) 'API redirects or more than five redirects are forbidden.'
                    $location = $response.Headers['Location']
                    Assert-QP (-not [string]::IsNullOrEmpty($location) -and $location.Length -le 8192) 'Invalid redirect location.'
                    $current = ([uri]::new([uri]$current, $location)).AbsoluteUri
                    Assert-QPDownloadUrl $current $Url
                    continue
                }
                Assert-QP ($code -eq 200) 'GitHub did not return HTTP 200.'
                Assert-QP ([string]::IsNullOrEmpty($response.ContentEncoding) -or $response.ContentEncoding -ceq 'identity') 'Encoded HTTPS transfers are forbidden.'
                Assert-QP ($response.ContentLength -ge -1 -and $response.ContentLength -le $Limit) 'HTTPS response exceeds its size limit.'
                $input = $response.GetResponseStream(); $output = [IO.MemoryStream]::new()
                try {
                    $buffer = New-Object byte[] 65536
                    while ($true) {
                        $remaining = $deadline - [int]$clock.ElapsedMilliseconds
                        Assert-QP ($remaining -gt 0) 'HTTPS download deadline expired.'
                        if ($input.CanTimeout) { $input.ReadTimeout = [Math]::Min(15000, $remaining) }
                        $count = $input.Read($buffer, 0, [Math]::Min($buffer.Length, $Limit + 1 - [int]$output.Length))
                        if ($count -eq 0) { break }
                        Assert-QP ($output.Length + $count -le $Limit) 'HTTPS response exceeds its size limit.'
                        $output.Write($buffer, 0, $count)
                    }
                    Assert-QP ($clock.ElapsedMilliseconds -le $deadline -and ($response.ContentLength -lt 0 -or $response.ContentLength -eq $output.Length)) 'Incomplete or timed-out HTTPS response.'
                    return ,($output.ToArray())
                } finally { $input.Dispose(); $output.Dispose() }
            } finally { if ($null -ne $response) { $response.Dispose() }; $request.Abort() }
        }
        throw 'Too many HTTPS redirects.'
    } finally { [Net.ServicePointManager]::SecurityProtocol = $oldTls; $clock.Stop() }
}

function Read-QPRelease {
    param([byte[]]$Bytes, [string]$Requested = '', [switch]$AllowFormat2)
    $value = ConvertFrom-QPJson $Bytes 1048576
    Assert-QP ($value['draft'] -is [bool] -and -not $value['draft'] -and $value['prerelease'] -is [bool] -and -not $value['prerelease']) 'A published stable Release is required.'
    $tag = $value['tag_name']; $version = ConvertTo-QPVersion $tag -Tagged
    Assert-QP ([string]::IsNullOrEmpty($Requested) -or $Requested -ceq $tag) 'GitHub returned the wrong version.'
    Assert-QP ($value['html_url'] -is [string] -and $value['html_url'] -ceq "https://github.com/LinyuFiee/QQON-Panel/releases/tag/$tag") 'Non-canonical Release URL.'
    $legacy = $AllowFormat2 -and $value['assets'] -is [object[]] -and $value['assets'].Count -eq 6
    $limits = Get-QPAssetLimits -Legacy:$legacy
    Assert-QP ($AllowFormat2 -or $version -ge [version]'0.5.0') 'Windows setup requires a published format 3 Release >= v0.5.0.'
    Assert-QP ($value['assets'] -is [object[]] -and $value['assets'].Count -eq $limits.Count) 'Release must contain exactly the nine format 3 assets; Windows requires >= v0.5.0.'
    $sizes = [hashtable]::new([StringComparer]::Ordinal)
    foreach ($asset in $value['assets']) {
        Assert-QP ($asset -is [hashtable] -and $asset['name'] -is [string]) 'Invalid Release asset.'
        $name = $asset['name']
        Assert-QP ($limits.ContainsKey($name) -and $name -cin @($limits.Keys) -and -not $sizes.ContainsKey($name)) 'Unknown or duplicate Release asset.'
        Assert-QP (Test-QPInteger $asset['size'] 1 $limits[$name]) 'Invalid Release asset size.'
        Assert-QP ($asset['browser_download_url'] -is [string] -and $asset['browser_download_url'] -ceq (Get-QPAssetUrl $tag $name)) 'Non-canonical asset URL.'
        Assert-QP ($asset['state'] -is [string] -and $asset['state'] -ceq 'uploaded') 'Release asset is not fully uploaded.'
        $sizes[$name] = $asset['size']
    }
    return @{ Tag = $tag; Sizes = $sizes; Legacy = [bool]$legacy }
}

function Read-QPChecksums {
    param([byte[]]$Bytes, [switch]$AllowFormat2)
    Assert-QP ($null -ne $Bytes -and $Bytes.Length -gt 0 -and $Bytes.Length -le 16384) 'Invalid SHA256SUMS size.'
    $text = [Text.UTF8Encoding]::new($false, $true).GetString($Bytes)
    Assert-QP ($text -cmatch '\A[\x00-\x7f]+\z' -and $text.EndsWith("`n", [StringComparison]::Ordinal)) 'SHA256SUMS must be ASCII with a final LF.'
    $lines = $text.Split([char]10)
    $legacy = $AllowFormat2 -and $lines.Count -eq 6
    $limits = Get-QPAssetLimits -Legacy:$legacy
    $sums = [hashtable]::new([StringComparer]::Ordinal)
    foreach ($line in $lines[0..($lines.Length - 2)]) {
        $m = [regex]::Match($line, '\A([0-9a-f]{64})  ([A-Za-z0-9_.-]+)\z')
        Assert-QP $m.Success 'Invalid SHA256SUMS line.'
        $name = $m.Groups[2].Value
        Assert-QP ($name -cne 'SHA256SUMS' -and $name -cin @($limits.Keys) -and -not $sums.ContainsKey($name)) 'Unknown or duplicate checksum asset.'
        $sums[$name] = $m.Groups[1].Value
    }
    Assert-QP ($sums.Count -eq $limits.Count - 1) 'SHA256SUMS must cover exactly the other eight format 3 assets.'
    return ,$sums
}

function Read-QPManifest {
    param([byte[]]$Bytes, [hashtable]$Sums, [hashtable]$Release, [switch]$AllowFormat2)
    Assert-QP ((Get-QPSha256 $Bytes) -ceq $Sums['release.json']) 'Manifest SHA-256 mismatch.'
    $value = ConvertFrom-QPJson $Bytes 32768
    Assert-QPFields $value @('format', 'version', 'tag', 'repository', 'dataSchema', 'commit', 'buildTime', 'binaries')
    $legacy = $AllowFormat2 -and (Test-QPInteger $value['format'] 2 2)
    Assert-QP ($legacy -or (Test-QPInteger $value['format'] 3 3)) 'Windows installation requires format 3 / >= v0.5.0. Format 2 is informational only.'
    Assert-QPIdentity $value
    Assert-QP ($value['tag'] -is [string] -and $value['tag'] -ceq $Release.Tag -and $value['version'] -ceq $Release.Tag.Substring(1)) 'Manifest release identity mismatch.'
    Assert-QP ([bool]$Release.Legacy -eq [bool]$legacy) 'Manifest and asset-set formats disagree.'
    $count = 3; if ($legacy) { $count = 2 }
    Assert-QP ($value['binaries'] -is [object[]] -and $value['binaries'].Count -eq $count) 'Unexpected binary record count.'
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($binary in $value['binaries']) {
        Assert-QPFields $binary @('os', 'arch', 'name', 'size', 'sha256', 'archive')
        Assert-QP ($binary['os'] -is [string] -and $binary['arch'] -is [string] -and $binary['name'] -is [string]) 'Invalid binary identity types.'
        $key = $binary['os'] + '/' + $binary['arch']
        $allowed = @('linux/amd64', 'linux/arm64'); if (-not $legacy) { $allowed += 'windows/amd64' }
        Assert-QP ($key -cin $allowed -and $seen.Add($key)) 'Unknown or duplicate binary platform.'
        $name = 'panel'; $archiveName = 'panel-linux-' + $binary['arch'] + '.tar.gz'
        if ($binary['os'] -ceq 'windows') { $name = 'panel.exe'; $archiveName = 'panel-windows-amd64.zip' }
        Assert-QP ($binary['name'] -ceq $name -and (Test-QPInteger $binary['size'] 1 67108864) -and $binary['sha256'] -is [string] -and $binary['sha256'] -cmatch '\A[0-9a-f]{64}\z') 'Invalid inner binary record.'
        $archive = $binary['archive']; Assert-QPFields $archive @('name', 'size', 'sha256')
        Assert-QP ($archive['name'] -is [string] -and $archive['name'] -ceq $archiveName -and (Test-QPInteger $archive['size'] 1 67108864)) 'Invalid archive record.'
        Assert-QP ($archive['sha256'] -is [string] -and $archive['sha256'] -cmatch '\A[0-9a-f]{64}\z' -and $archive['sha256'] -ceq $Sums[$archiveName] -and $archive['size'] -eq $Release.Sizes[$archiveName]) 'Archive manifest/checksum/API mismatch.'
    }
    return ,$value
}

function Get-QPReleaseMetadata {
    param([string]$Version = '')
    $url = 'https://api.github.com/repos/LinyuFiee/QQON-Panel/releases/latest'
    if ($Version) { [void](ConvertTo-QPVersion $Version -Tagged); $url = 'https://api.github.com/repos/LinyuFiee/QQON-Panel/releases/tags/' + $Version }
    $release = Read-QPRelease (Read-QPDownload $url 1048576 -Api) $Version
    $sumBytes = Read-QPDownload (Get-QPAssetUrl $release.Tag 'SHA256SUMS') 16384
    Assert-QP ($sumBytes.Length -eq $release.Sizes['SHA256SUMS']) 'Checksum asset size differs from the API.'
    $sums = Read-QPChecksums $sumBytes
    $manifestBytes = Read-QPDownload (Get-QPAssetUrl $release.Tag 'release.json') 32768
    Assert-QP ($manifestBytes.Length -eq $release.Sizes['release.json']) 'Manifest asset size differs from the API.'
    $manifest = Read-QPManifest $manifestBytes $sums $release
    return @{ Release = $release; Sums = $sums; Manifest = $manifest }
}

function Read-QPVerifiedAsset {
    param([hashtable]$Metadata, [string]$Name)
    $limits = Get-QPAssetLimits
    Assert-QP ($Name -cin @($limits.Keys) -and $Name -cne 'SHA256SUMS') 'Unknown checksummed asset.'
    $bytes = Read-QPDownload (Get-QPAssetUrl $Metadata.Release.Tag $Name) $limits[$Name]
    Assert-QP ($bytes.Length -eq $Metadata.Release.Sizes[$Name] -and (Get-QPSha256 $bytes) -ceq $Metadata.Sums[$Name]) 'Release asset SHA-256 or size mismatch.'
    return ,$bytes
}

function Assert-QPPlainPath {
    param([string]$Path, [switch]$AllowMissing)
    Assert-QP ($Path -cmatch '\A[A-Za-z]:\\' -and $Path.Substring(2) -notmatch ':' -and $Path -notmatch '[\x00-\x1f<>"|?*]') 'Only absolute local drive paths are supported.'
    $full = [IO.Path]::GetFullPath($Path)
    Assert-QP ($full.TrimEnd('\') -ceq $Path.TrimEnd('\')) 'Non-canonical filesystem path.'
    $parts = $Path.Substring(3).Split([char]'\')
    $current = $Path.Substring(0, 3)
    foreach ($part in $parts) {
        if ($part.Length -eq 0) { continue }
        Assert-QP ($part -notin @('.', '..') -and $part -notmatch '[ .]\z') 'Ambiguous filesystem component.'
        $current = [IO.Path]::Combine($current, $part)
        try { $attributes = [IO.File]::GetAttributes($current) }
        catch [IO.FileNotFoundException] { Assert-QP ([bool]$AllowMissing) 'Required file is missing.'; continue }
        catch [IO.DirectoryNotFoundException] { Assert-QP ([bool]$AllowMissing) 'Required directory is missing.'; continue }
        Assert-QP (($attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) 'Reparse points, junctions and symlinks are forbidden.'
    }
}

function Assert-QPNativeHost {
    param([switch]$Administrator)
    Assert-QP ($PSVersionTable.PSVersion -ge [version]'5.1' -and $PSVersionTable.PSEdition -ceq 'Desktop' -and [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT -and [Environment]::Is64BitProcess) 'Use native 64-bit Windows PowerShell 5.1 (not PowerShell Core, WSL or 32-bit PowerShell).'
    if ($Administrator) {
        $principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
        Assert-QP ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) 'Open Windows PowerShell as Administrator.'
    }
    $os = Get-CimInstance -ClassName Win32_OperatingSystem -OperationTimeoutSec 10 -ErrorAction Stop
    $build = [int]$os.BuildNumber
    $clientBuilds = @(10240, 10586, 14393, 15063, 16299, 17134, 17763, 18362, 18363, 19041, 19042, 19043, 19044, 19045, 22000, 22621, 22631, 26100, 26200)
    Assert-QP ($os.Version.StartsWith('10.0.', [StringComparison]::Ordinal) -and (($os.ProductType -eq 1 -and $build -in $clientBuilds) -or ($os.ProductType -in @(2, 3) -and $build -in @(17763, 20348, 26100)))) 'Only Windows 10/11 and Server 2019/2022/2025 are supported; this OS/build is not supported.'
    $processors = @(Get-CimInstance -ClassName Win32_Processor -OperationTimeoutSec 10 -ErrorAction Stop)
    Assert-QP ($processors.Count -gt 0 -and @($processors | Where-Object { $_.Architecture -ne 9 }).Count -eq 0) 'Only native amd64 Windows is supported, not ARM64 emulation.'
    foreach ($base in @([Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFiles), [Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData))) {
        Assert-QPPlainPath $base
        $drive = [IO.DriveInfo]::new([IO.Path]::GetPathRoot($base))
        Assert-QP ($drive.IsReady -and $drive.DriveType -eq [IO.DriveType]::Fixed -and $drive.DriveFormat -ceq 'NTFS') 'ProgramFiles and ProgramData must be on local fixed NTFS volumes.'
    }
}

function ConvertTo-QPArgument {
    param([string]$Value)
    Assert-QP ($Value -notmatch '[\x00\r\n]') 'Invalid process argument.'
    # CommandLineToArgvW quoting; no shell is involved.
    $escaped = [regex]::Replace($Value, '(\\*)"', '$1$1\"')
    $escaped = [regex]::Replace($escaped, '(\\+)\z', '$1$1')
    return '"' + $escaped + '"'
}

# END SHARED RELEASE READER

function Get-QPPaths {
    $code = [IO.Path]::Combine([Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFiles), 'QQON-Panel')
    $state = [IO.Path]::Combine([Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData), 'QQON-Panel')
    return @{
        Code = $code; State = $state; Data = [IO.Path]::Combine($state, 'data')
        Binary = [IO.Path]::Combine($code, 'panel.exe'); Previous = [IO.Path]::Combine($code, 'panel.previous.exe')
        Candidate = [IO.Path]::Combine($code, 'panel.next.exe'); Installer = [IO.Path]::Combine($code, 'install.ps1')
        Updater = [IO.Path]::Combine($code, 'update.ps1'); Config = [IO.Path]::Combine($state, 'install.json')
        Journal = [IO.Path]::Combine($state, 'transaction.json')
        Tls = [IO.Path]::Combine($state, 'tls'); Backups = [IO.Path]::Combine($state, 'backups')
        Shortcut = [IO.Path]::Combine($code, 'qqon.ps1'); Command = [IO.Path]::Combine($code, 'qqon.cmd')
    }
}

function Get-QPServiceSid {
    # Windows virtual service SID = S-1-5-80 + SHA1(upper-case UTF-16 service name).
    $sha = [Security.Cryptography.SHA1]::Create()
    try { $hash = $sha.ComputeHash([Text.Encoding]::Unicode.GetBytes('QQONPANEL')) } finally { $sha.Dispose() }
    $parts = @(for ($i = 0; $i -lt 20; $i += 4) { [BitConverter]::ToUInt32($hash, $i).ToString([Globalization.CultureInfo]::InvariantCulture) })
    return [Security.Principal.SecurityIdentifier]::new('S-1-5-80-' + ($parts -join '-'))
}

function Get-QPRights {
    param([ValidateSet('Code', 'State', 'Data', 'Admin', 'Tls')][string]$Kind)
    $map = @{ 'S-1-5-18' = [long][Security.AccessControl.FileSystemRights]::FullControl; 'S-1-5-32-544' = [long][Security.AccessControl.FileSystemRights]::FullControl }
    if ($Kind -ne 'Admin') {
        $rights = [Security.AccessControl.FileSystemRights]::ReadAndExecute
        if ($Kind -eq 'Data') { $rights = [Security.AccessControl.FileSystemRights]::FullControl }
        if ($Kind -eq 'State') { $rights = [Security.AccessControl.FileSystemRights]::Traverse }
        $map[(Get-QPServiceSid).Value] = [long]$rights
    }
    return $map
}

function New-QPSecurity {
    param([ValidateSet('Code', 'State', 'Data', 'Admin', 'Tls')][string]$Kind, [switch]$File)
    if ($File) { $acl = [Security.AccessControl.FileSecurity]::new() } else { $acl = [Security.AccessControl.DirectorySecurity]::new() }
    $acl.SetAccessRuleProtection($true, $false)
    $acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
    $rights = Get-QPRights $Kind
    foreach ($sid in $rights.Keys) {
        $inherit = [Security.AccessControl.InheritanceFlags]::None
        if (-not $File -and -not ($Kind -eq 'State' -and $sid -eq (Get-QPServiceSid).Value)) { $inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit' }
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid), [Security.AccessControl.FileSystemRights]$rights[$sid], $inherit, [Security.AccessControl.PropagationFlags]::None, [Security.AccessControl.AccessControlType]::Allow))
    }
    return $acl
}

function Assert-QPAcl {
    param([string]$Path, [ValidateSet('Code', 'State', 'Data', 'Admin', 'Tls')][string]$Kind, [switch]$File, [switch]$Root)
    Assert-QPPlainPath $Path
    Assert-QP (($File -and [IO.File]::Exists($Path)) -or (-not $File -and [IO.Directory]::Exists($Path))) 'Expected regular file or directory.'
    if ($File) { $acl = [IO.File]::GetAccessControl($Path) } else { $acl = [IO.Directory]::GetAccessControl($Path) }
    $owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
    $owners = @('S-1-5-18', 'S-1-5-32-544'); if ($Kind -eq 'Data' -and -not $Root) { $owners += (Get-QPServiceSid).Value }
    Assert-QP ($owner -in $owners) ('Unsafe owner on ' + $Path)
    if ($Root -or ($Kind -eq 'Tls' -and $File)) { Assert-QP $acl.AreAccessRulesProtected ('Unprotected directory ACL: ' + $Path) }
    $allowed = Get-QPRights $Kind
    $effective = @{}
    foreach ($rule in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
        $sid = $rule.IdentityReference.Value
        Assert-QP ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $allowed.ContainsKey($sid)) ('Broad or deny ACL on ' + $Path)
        $bits = [long]$rule.FileSystemRights
        # Windows adds SYNCHRONIZE to explicit allow rules. It grants no data access.
        $max = [long]$allowed[$sid] -bor [long][Security.AccessControl.FileSystemRights]::Synchronize
        Assert-QP (($bits -band (-bnot $max)) -eq 0 -and $bits -gt 0) ('Excess access on ' + $Path)
        Assert-QP ($rule.PropagationFlags -eq [Security.AccessControl.PropagationFlags]::None) 'Unexpected inherit-only or no-propagate ACL.'
        if (-not $effective.ContainsKey($sid)) { $effective[$sid] = [long]0 }
        $effective[$sid] = $effective[$sid] -bor $bits
        if ($Root -and -not ($Kind -eq 'State' -and $sid -eq (Get-QPServiceSid).Value)) {
            Assert-QP (($rule.InheritanceFlags -band [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit') -eq [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit') 'Private root must secure future children through inheritance.'
        }
    }
    foreach ($sid in $allowed.Keys) {
        # A stricter Admin/SYSTEM-only ACL is also safe. System layout validation
        # separately uses Kind Admin to forbid every legacy service ACE.
        if ($sid -ceq (Get-QPServiceSid).Value -and -not $effective.ContainsKey($sid)) { continue }
        Assert-QP ($effective.ContainsKey($sid) -and ($effective[$sid] -band [long]$allowed[$sid]) -eq [long]$allowed[$sid]) ('Missing required private ACL entry on ' + $Path)
    }
}

function Assert-QPInstallParent {
    param([string]$Path)
    Assert-QPPlainPath $Path
    $acl = [IO.Directory]::GetAccessControl($Path)
    $trusted = @('S-1-5-18', 'S-1-5-32-544', 'S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464')
    Assert-QP ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -in $trusted) 'Untrusted ProgramFiles/ProgramData owner.'
    $danger = [long][Security.AccessControl.FileSystemRights]'Delete, DeleteSubdirectoriesAndFiles, ChangePermissions, TakeOwnership'
    foreach ($rule in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
        if ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $rule.IdentityReference.Value -notin $trusted -and ($rule.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) -eq 0) {
            Assert-QP (([long]$rule.FileSystemRights -band $danger) -eq 0) 'An unprivileged account can replace an installation parent.'
        }
    }
}

function New-QPPrivateDirectory {
    param([string]$Path, [ValidateSet('Code', 'State', 'Data', 'Admin', 'Tls')][string]$Kind)
    Assert-QPPlainPath $Path -AllowMissing
    Assert-QP (-not [IO.Directory]::Exists($Path) -and -not [IO.File]::Exists($Path)) ('Refusing preexisting installation path: ' + $Path)
    [IO.DirectoryInfo]::new($Path).Create((New-QPSecurity $Kind))
    Assert-QPAcl $Path $Kind -Root
}

function Read-QPFile {
    param([string]$Path, [int]$Limit, [switch]$Regular)
    Assert-QPPlainPath $Path
    if ($Regular) { Initialize-QPProcessRunner; $stream = [QQONPanel.NativeProcess]::OpenRegularRead($Path) }
    else { $stream = [IO.FileStream]::new($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read) }
    try {
        Assert-QP ($stream.Length -gt 0 -and $stream.Length -le $Limit) 'Local file is empty or exceeds its size limit.'
        $bytes = New-Object byte[] ([int]$stream.Length)
        $offset = 0
        while ($offset -lt $bytes.Length) { $read = $stream.Read($bytes, $offset, $bytes.Length - $offset); Assert-QP ($read -gt 0) 'Truncated local file.'; $offset += $read }
        Assert-QP ($stream.ReadByte() -eq -1) 'Local file changed during read.'
        return ,$bytes
    } finally { $stream.Dispose() }
}

function Get-QPFileHash {
    param([string]$Path, [int]$Limit = 67108864)
    Assert-QPPlainPath $Path
    $stream = [IO.FileStream]::new($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        Assert-QP ($stream.Length -gt 0 -and $stream.Length -le $Limit) 'Local file size is invalid.'
        return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '').ToLowerInvariant()
    } finally { $sha.Dispose(); $stream.Dispose() }
}

function Write-QPNewFile {
    param([string]$Path, [byte[]]$Bytes, [ValidateSet('Code', 'Admin')][string]$Kind = 'Admin')
    Assert-QPPlainPath $Path -AllowMissing
    $stream = [IO.FileStream]::new($Path, [IO.FileMode]::CreateNew, [Security.AccessControl.FileSystemRights]::Write, [IO.FileShare]::None, 65536, [IO.FileOptions]::WriteThrough, (New-QPSecurity $Kind -File))
    try { $stream.Write($Bytes, 0, $Bytes.Length); $stream.Flush($true) } finally { $stream.Dispose() }
    Assert-QPAcl $Path $Kind -File
}

function ConvertTo-QPJsonBytes {
    param([hashtable]$Value)
    return ,([Text.UTF8Encoding]::new($false).GetBytes((ConvertTo-Json -InputObject $Value -Depth 8 -Compress) + "`n"))
}

function Write-QPAtomicJson {
    param([string]$Path, [hashtable]$Value)
    Assert-QPPlainPath $Path -AllowMissing
    $temporary = $Path + '.write-' + [guid]::NewGuid().ToString('N')
    try {
        Write-QPNewFile $temporary (ConvertTo-QPJsonBytes $Value) 'Admin'
        if ([IO.File]::Exists($Path)) { Assert-QPAcl $Path 'Admin' -File; [IO.File]::Replace($temporary, $Path, [NullString]::Value) }
        else { [IO.File]::Move($temporary, $Path) }
    } finally { if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) } }
}

function Remove-QPManagedFile {
    param([string]$Path, [ValidateSet('Code', 'Admin')][string]$Kind = 'Code')
    if ([IO.File]::Exists($Path)) { Assert-QPAcl $Path $Kind -File; [IO.File]::Delete($Path) }
    else { Assert-QPPlainPath $Path -AllowMissing; Assert-QP (-not [IO.Directory]::Exists($Path)) 'Expected a managed file, not a directory.' }
}

function Assert-QPPE {
    param([string]$Path)
    $bytes = Read-QPFile $Path 67108864
    Assert-QP ($bytes.Length -ge 256 -and $bytes[0] -eq 0x4d -and $bytes[1] -eq 0x5a) 'Windows binary is not an MZ/PE executable.'
    $offset = [BitConverter]::ToUInt32($bytes, 0x3c)
    Assert-QP ($offset -ge 64 -and $offset + 24 + 112 -le $bytes.Length) 'Invalid PE header offset.'
    $offset = [int]$offset
    Assert-QP ([BitConverter]::ToUInt32($bytes, $offset) -eq 0x00004550 -and [BitConverter]::ToUInt16($bytes, $offset + 4) -eq 0x8664) 'Only AMD64 PE is supported.'
    $sections = [BitConverter]::ToUInt16($bytes, $offset + 6)
    $optional = [BitConverter]::ToUInt16($bytes, $offset + 20)
    $flags = [BitConverter]::ToUInt16($bytes, $offset + 22)
    Assert-QP ($sections -gt 0 -and $sections -le 96 -and $optional -ge 112 -and $offset + 24 + $optional + 40 * $sections -le $bytes.Length) 'Invalid PE section/optional header.'
    Assert-QP (($flags -band 2) -ne 0 -and ($flags -band 0x2000) -eq 0 -and [BitConverter]::ToUInt16($bytes, $offset + 24) -eq 0x20b) 'Expected PE32+ executable, not DLL or 32-bit image.'
    Assert-QP ([BitConverter]::ToUInt16($bytes, $offset + 24 + 68) -eq 3) 'Expected Windows console PE subsystem.'
    for ($i = 0; $i -lt $sections; $i++) {
        $header = $offset + 24 + $optional + 40 * $i
        $size = [BitConverter]::ToUInt32($bytes, $header + 16); $raw = [BitConverter]::ToUInt32($bytes, $header + 20)
        Assert-QP ([long]$raw + $size -le $bytes.Length) 'PE section exceeds the file.'
    }
}

function Get-QPFileCrc32 {
    param([string]$Path)
    Initialize-QPProcessRunner
    return [QQONPanel.NativeProcess]::Crc32($Path)
}

function Expand-QPWindowsZip {
    param([byte[]]$Bytes, [hashtable]$Expected, [string]$Destination)
    Assert-QP ($Bytes.Length -ge 116 -and $Bytes.Length -le 67108864 -and $Bytes.Length -eq $Expected['archive']['size'] -and (Get-QPSha256 $Bytes) -ceq $Expected['archive']['sha256']) 'Windows ZIP archive size/SHA-256 mismatch.'
    # Parse the fixed one-entry non-ZIP64 layout before asking .NET to decompress.
    $end = $Bytes.Length - 22
    Assert-QP ([BitConverter]::ToUInt32($Bytes, $end) -eq 0x06054b50 -and [BitConverter]::ToUInt16($Bytes, $end + 20) -eq 0) 'ZIP comments/trailers are forbidden.'
    Assert-QP ([BitConverter]::ToUInt16($Bytes, $end + 4) -eq 0 -and [BitConverter]::ToUInt16($Bytes, $end + 6) -eq 0 -and [BitConverter]::ToUInt16($Bytes, $end + 8) -eq 1 -and [BitConverter]::ToUInt16($Bytes, $end + 10) -eq 1) 'ZIP must be single-volume with exactly one entry.'
    $central = [long][BitConverter]::ToUInt32($Bytes, $end + 16)
    $centralSize = [long][BitConverter]::ToUInt32($Bytes, $end + 12)
    Assert-QP ($central -ge 39 -and $centralSize -eq 55 -and $central + $centralSize -eq $end) 'Unexpected ZIP central directory, extra fields or ZIP64.'
    $central = [int]$central
    Assert-QP ([BitConverter]::ToUInt32($Bytes, $central) -eq 0x02014b50 -and [BitConverter]::ToUInt16($Bytes, $central + 28) -eq 9 -and [BitConverter]::ToUInt16($Bytes, $central + 30) -eq 0 -and [BitConverter]::ToUInt16($Bytes, $central + 32) -eq 0 -and [BitConverter]::ToUInt16($Bytes, $central + 34) -eq 0 -and [BitConverter]::ToUInt32($Bytes, $central + 42) -eq 0) 'Non-regular ZIP central entry.'
    Assert-QP ([Text.Encoding]::ASCII.GetString($Bytes, $central + 46, 9) -ceq 'panel.exe') 'ZIP may contain only top-level panel.exe.'
    $created = [BitConverter]::ToUInt16($Bytes, $central + 4)
    $needed = [BitConverter]::ToUInt16($Bytes, $central + 6)
    $flags = [BitConverter]::ToUInt16($Bytes, $central + 8)
    $method = [BitConverter]::ToUInt16($Bytes, $central + 10)
    $compressed = [long][BitConverter]::ToUInt32($Bytes, $central + 20)
    $expanded = [long][BitConverter]::ToUInt32($Bytes, $central + 24)
    $attributes = [BitConverter]::ToUInt32($Bytes, $central + 38)
    Assert-QP (($flags -band 0xf7ff) -eq 0 -and $needed -le 20 -and $method -in @(0, 8)) 'Encrypted ZIP, descriptors or unsupported compression are forbidden.'
    Assert-QP (($created -shr 8) -in @(0, 3) -and ($attributes -band 0x418) -eq 0) 'ZIP directories, reparse points and volume entries are forbidden.'
    if (($created -shr 8) -eq 3) { Assert-QP ((($attributes -shr 16) -band 0xf000) -eq 0x8000) 'ZIP Unix entry must be a regular file, not a link.' }
    Assert-QP ($expanded -ge 1 -and $expanded -le 67108864 -and $expanded -eq $Expected['size'] -and $compressed -ge 1 -and 39 + $compressed -eq $central) 'ZIP size limit or inner manifest size mismatch.'
    Assert-QP ([BitConverter]::ToUInt32($Bytes, 0) -eq 0x04034b50 -and [BitConverter]::ToUInt16($Bytes, 26) -eq 9 -and [BitConverter]::ToUInt16($Bytes, 28) -eq 0 -and [Text.Encoding]::ASCII.GetString($Bytes, 30, 9) -ceq 'panel.exe') 'ZIP local path/header differs from the fixed entry.'
    foreach ($pair in @(@(4, 6, 2), @(6, 8, 2), @(8, 10, 2), @(10, 12, 2), @(12, 14, 2), @(14, 16, 4), @(18, 20, 4), @(22, 24, 4))) {
        for ($i = 0; $i -lt $pair[2]; $i++) { Assert-QP ($Bytes[$pair[0] + $i] -eq $Bytes[$central + $pair[1] + $i]) 'ZIP local/central metadata mismatch.' }
    }
    Add-Type -AssemblyName System.IO.Compression -ErrorAction Stop
    $memory = [IO.MemoryStream]::new($Bytes, $false)
    $archive = [IO.Compression.ZipArchive]::new($memory, [IO.Compression.ZipArchiveMode]::Read, $false)
    $output = $null; $input = $null
    try {
        Assert-QP ($archive.Entries.Count -eq 1 -and $archive.Entries[0].FullName -ceq 'panel.exe' -and $archive.Entries[0].Length -eq $expanded) 'Unexpected .NET ZIP entry.'
        Assert-QPPlainPath $Destination -AllowMissing
        $output = [IO.FileStream]::new($Destination, [IO.FileMode]::CreateNew, [Security.AccessControl.FileSystemRights]::Write, [IO.FileShare]::None, 65536, [IO.FileOptions]::WriteThrough, (New-QPSecurity 'Code' -File))
        $input = $archive.Entries[0].Open(); $buffer = New-Object byte[] 65536; [long]$total = 0
        $clock = [Diagnostics.Stopwatch]::StartNew()
        while ($true) {
            Assert-QP ($clock.Elapsed.TotalSeconds -lt 120) 'ZIP decompression deadline expired.'
            $count = $input.Read($buffer, 0, [Math]::Min($buffer.Length, [int]($expanded + 1 - $total)))
            if ($count -eq 0) { break }
            $total += $count; Assert-QP ($total -le $expanded) 'ZIP expansion exceeds the declared limit.'
            $output.Write($buffer, 0, $count)
        }
        Assert-QP ($total -eq $expanded) 'Truncated ZIP payload.'
        $output.Flush($true)
    } finally { if ($null -ne $input) { $input.Dispose() }; if ($null -ne $output) { $output.Dispose() }; $archive.Dispose(); $memory.Dispose() }
    Assert-QP ((Get-QPFileHash $Destination) -ceq $Expected['sha256']) 'Inner panel.exe SHA-256 mismatch.'
    Assert-QP ((Get-QPFileCrc32 $Destination) -eq [BitConverter]::ToUInt32($Bytes, $central + 16)) 'ZIP payload CRC32 mismatch.'
    Assert-QPPE $Destination
}

function Initialize-QPProcessRunner {
    if ($null -ne ('QQONPanel.NativeProcess' -as [type])) { return }
    # Inbox .NET C# compiler; bounded output without shell evaluation or a download dependency.
    Add-Type -TypeDefinition @'
using System;
using System.Diagnostics;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;
namespace QQONPanel {
  public static class NativeProcess {
    [StructLayout(LayoutKind.Sequential)] struct FileInformation {
      public uint Attributes, CreationLow, CreationHigh, AccessLow, AccessHigh, WriteLow, WriteHigh, Volume, SizeHigh, SizeLow, Links, IndexHigh, IndexLow;
    }
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern SafeFileHandle CreateFile(string name, uint access, uint share, IntPtr security, uint mode, uint flags, IntPtr template);
    [DllImport("kernel32.dll", SetLastError=true)] static extern bool GetFileInformationByHandle(SafeFileHandle handle, out FileInformation information);
    [DllImport("kernel32.dll", SetLastError=true)] static extern uint GetFileType(SafeFileHandle handle);
    public static FileStream OpenRegularRead(string path) {
      SafeFileHandle handle = CreateFile(path, 0x80000000, 1, IntPtr.Zero, 3, 0x00200000, IntPtr.Zero);
      try {
        FileInformation information;
        if (handle.IsInvalid || GetFileType(handle) != 1 || !GetFileInformationByHandle(handle, out information) || information.Links != 1 || (information.Attributes & (0x10U|0x400U|0x40U)) != 0)
          throw new IOException("来源必须是单链接普通磁盘文件；拒绝链接、重解析点或设备。[QP_REGULAR]");
        return new FileStream(handle, FileAccess.Read);
      } catch { handle.Dispose(); throw; }
    }
    public static uint Crc32(string path) {
      uint[] table = new uint[256];
      for (uint i = 0; i < 256; i++) { uint c = i; for (int j = 0; j < 8; j++) c = (c & 1) != 0 ? 0xedb88320U ^ (c >> 1) : c >> 1; table[i] = c; }
      uint crc = 0xffffffffU;
      using (FileStream input = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read)) {
        if (input.Length < 1 || input.Length > 67108864) throw new IOException("Invalid CRC input size.");
        byte[] buffer = new byte[65536]; int n;
        while ((n = input.Read(buffer, 0, buffer.Length)) != 0) for (int i = 0; i < n; i++) crc = table[(crc ^ buffer[i]) & 255] ^ (crc >> 8);
      }
      return ~crc;
    }
    static void Pump(Stream source, MemoryStream dest, int limit) {
      byte[] buffer = new byte[4096]; int n;
      while ((n = source.Read(buffer, 0, buffer.Length)) != 0) {
        if (dest.Length + n > limit) throw new IOException("Subprocess output exceeds limit.");
        dest.Write(buffer, 0, n);
      }
    }
    public static byte[] Run(string file, string arguments, string cwd, int timeout, int limit) {
      using (Process p = new Process()) using (MemoryStream stdout = new MemoryStream()) using (MemoryStream stderr = new MemoryStream()) {
        p.StartInfo = new ProcessStartInfo(file, arguments) { UseShellExecute = false, CreateNoWindow = true,
          RedirectStandardOutput = true, RedirectStandardError = true, RedirectStandardInput = true, WorkingDirectory = cwd };
        p.StartInfo.EnvironmentVariables.Remove("GODEBUG");
        p.StartInfo.EnvironmentVariables.Remove("GOMEMLIMIT");
        p.Start(); p.StandardInput.Close();
        Task a = Task.Run(() => Pump(p.StandardOutput.BaseStream, stdout, limit));
        Task b = Task.Run(() => Pump(p.StandardError.BaseStream, stderr, limit));
        Stopwatch watch = Stopwatch.StartNew();
        try {
          while (!(p.HasExited && a.IsCompleted && b.IsCompleted)) {
            if (a.IsFaulted || b.IsFaulted) throw new IOException("Subprocess output rejected.");
            if (watch.ElapsedMilliseconds >= timeout) throw new TimeoutException("Subprocess deadline expired.");
            Thread.Sleep(10);
          }
          Task.WaitAll(a,b);
          if (p.ExitCode != 0) throw new IOException("Subprocess failed with exit code " + p.ExitCode + ".");
          return stdout.ToArray();
        } finally {
          if (!p.HasExited) { p.Kill(); p.WaitForExit(5000); }
        }
      }
    }
  }
}
'@ -ErrorAction Stop
}

function Invoke-QPProcess {
    param([string]$File, [string[]]$Arguments, [int]$Timeout = 15000, [int]$Limit = 32768)
    Initialize-QPProcessRunner
    Assert-QPPlainPath $File
    $joined = (@($Arguments | ForEach-Object { ConvertTo-QPArgument $_ }) -join ' ')
    return ,([QQONPanel.NativeProcess]::Run($File, $joined, [Environment]::SystemDirectory, $Timeout, $Limit))
}

function Assert-QPBuildInfo {
    param([byte[]]$Bytes, [hashtable]$Expected)
    $value = ConvertFrom-QPJson $Bytes 32768
    Assert-QPFields $value @('version', 'commit', 'buildTime', 'repository', 'dataSchema', 'os', 'arch')
    Assert-QPIdentity $value
    Assert-QP ($value['os'] -is [string] -and $value['os'] -ceq 'windows' -and $value['arch'] -is [string] -and $value['arch'] -ceq 'amd64') 'Build-info does not identify Windows amd64.'
    foreach ($key in @('version', 'commit', 'buildTime', 'repository', 'dataSchema')) { Assert-QP ($value[$key] -ceq $Expected[$key]) 'Binary build-info differs from verified metadata.' }
}

function Assert-QPBinary {
    param([string]$Path, [hashtable]$Config, [switch]$ReadOnly)
    Assert-QPAcl $Path 'Code' -File
    Assert-QP (([IO.FileInfo]::new($Path)).Length -eq $Config['binarySize'] -and (Get-QPFileHash $Path) -ceq $Config['binarySHA256']) 'Installed binary hash/size differs from trusted metadata.'
    Assert-QPPE $Path
    if (-not $ReadOnly) { Assert-QPBuildInfo (Invoke-QPProcess $Path @('-build-info') 10000) $Config }
}

function Get-QPOrigin {
    param([int]$Port, [string]$Origin)
    Assert-QP ($Port -ge 1024 -and $Port -le 65535) 'Port must be 1024-65535.'
    if ([string]::IsNullOrEmpty($Origin)) { return @{ Origin = ''; Host = '127.0.0.1:' + $Port } }
    $match = [regex]::Match($Origin, '\Ahttps://([a-zA-Z0-9.-]+)(?::([0-9]{1,5}))?/?\z')
    Assert-QP $match.Success 'Origin must be an existing HTTPS domain origin; no credentials, path, query, fragment, IP or HTTP.'
    $name = $match.Groups[1].Value.ToLowerInvariant()
    Assert-QP ($name.Length -le 253 -and $name.Contains('.') -and -not $name.EndsWith('.')) 'HTTPS origin must be a DNS domain.'
    foreach ($label in $name.Split([char]'.')) { Assert-QP ($label -cmatch '\A[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\z') 'Invalid HTTPS DNS label.' }
    $ip = $null; Assert-QP (-not [Net.IPAddress]::TryParse($name, [ref]$ip)) 'Use a DNS domain, not an IP address, for the existing HTTPS proxy.'
    $hostValue = $name
    if ($match.Groups[2].Success) {
        $portText = $match.Groups[2].Value; $httpsPort = [int]$portText
        Assert-QP ($httpsPort -ge 1 -and $httpsPort -le 65535 -and $portText -ceq $httpsPort.ToString()) 'Invalid HTTPS origin port.'
        if ($httpsPort -ne 443) { $hostValue += ':' + $httpsPort }
    }
    return @{ Origin = 'https://' + $hostValue; Host = $hostValue }
}

function ConvertTo-QPHost {
    param([string]$Name, [switch]$PublicIPv4)
    Assert-QP ($Name.Length -gt 0 -and $Name.Length -le 253 -and $Name -cmatch '\A[a-zA-Z0-9.-]+\z') '主机名必须是规范 IPv4 或 ASCII 域名，不得含路径、端口或空白。'
    $ip = $null
    if ([Net.IPAddress]::TryParse($Name, [ref]$ip)) {
        Assert-QP ($ip.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork -and $ip.ToString() -ceq $Name) '只支持规范的点分十进制 IPv4，不支持 IPv6 或缩写地址。'
        if ($PublicIPv4) {
            $b = $ip.GetAddressBytes()
            $reserved = $b[0] -in @(0, 10, 127) -or $b[0] -ge 224 -or
                ($b[0] -eq 100 -and $b[1] -ge 64 -and $b[1] -le 127) -or
                ($b[0] -eq 169 -and $b[1] -eq 254) -or ($b[0] -eq 172 -and $b[1] -ge 16 -and $b[1] -le 31) -or
                ($b[0] -eq 192 -and ($b[1] -eq 168 -or ($b[1] -eq 0 -and $b[2] -in @(0, 2)) -or ($b[1] -eq 88 -and $b[2] -eq 99))) -or
                ($b[0] -eq 198 -and ($b[1] -in @(18, 19) -or ($b[1] -eq 51 -and $b[2] -eq 100))) -or
                ($b[0] -eq 203 -and $b[1] -eq 0 -and $b[2] -eq 113)
            Assert-QP (-not $reserved) '自签公网模式需要全局可路由 IPv4；拒绝内网、回环、链路本地、共享、文档、组播或保留地址。'
        }
        return $Name
    }
    Assert-QP (-not $PublicIPv4) '自签公网模式请确认并输入服务器的公网 IPv4，不使用外部地址发现服务。'
    $canonical = $Name.ToLowerInvariant()
    Assert-QP ($canonical.Contains('.') -and -not $canonical.EndsWith('.') -and $canonical -cnotmatch '\A[0-9.]+\z') '请输入完整的 ASCII 域名或规范 IPv4。'
    foreach ($label in $canonical.Split([char]'.')) { Assert-QP ($label -cmatch '\A[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\z') '域名标签格式无效。' }
    return $canonical
}

function Read-QPUtcTime {
    param([string]$Value)
    Assert-QP ($Value -cmatch '\A[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,7})?Z\z') '证书到期时间必须为 RFC3339 UTC。'
    $date = [datetimeoffset]::MinValue
    Assert-QP ([datetimeoffset]::TryParse($Value, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal, [ref]$date)) '证书到期日期无效。'
    return $date.ToUniversalTime()
}

function Get-QPAccess {
    param([string]$Mode, [int]$Port, [string]$Name = '', [string]$Origin = '')
    Assert-QP ($Port -ge 1024 -and $Port -le 65535) '端口必须为 1024–65535，默认 8443。'
    Assert-QP ($Mode -cin @('local', 'proxy', 'public-selfsigned', 'public-certificate')) '访问模式无效。'
    if ($Mode -cin @('local', 'proxy')) {
        Assert-QP (-not $Name) '本地和已有代理模式不接受公网主机参数。'
        if ($Mode -ceq 'local') { Assert-QP (-not $Origin) '本地模式不能设置外部 HTTPS 地址。' }
        else { Assert-QP ([bool]$Origin) '代理模式必须填写已配置好的 HTTPS 域名地址；本机不会创建代理。' }
        $info = Get-QPOrigin $Port $Origin
        return @{ accessMode = $Mode; listen = '127.0.0.1:' + $Port; origin = $info.Origin; healthHost = $info.Host; host = '' }
    }
    Assert-QP (-not $Origin) '公网模式的 HTTPS 地址由主机和端口生成，不接受任意 origin。'
    $canonical = ConvertTo-QPHost $Name -PublicIPv4:($Mode -ceq 'public-selfsigned')
    $authority = $canonical + ':' + $Port
    return @{ accessMode = $Mode; listen = '0.0.0.0:' + $Port; origin = 'https://' + $authority; healthHost = $authority; host = $canonical }
}

function Get-QPPrivilegeMode {
    param([hashtable]$Config)
    if ($Config['format'] -ne 3) { return 'restricted' }
    Assert-QP ($Config['privilegeMode'] -is [string] -and $Config['privilegeMode'] -cin @('restricted', 'system')) '权限模式无效。[QP_PRIVILEGE]'
    return $Config['privilegeMode']
}

function Get-QPStorageKind {
    param([hashtable]$Config, [string]$Kind)
    if ((Get-QPPrivilegeMode $Config) -ceq 'system') { return 'Admin' }
    return $Kind
}

function Read-QPPrivilegeConsent {
    param($Value)
    Assert-QPFields $Value @('format', 'mode', 'identity', 'noticeVersion', 'acceptedAt', 'acceptedBy', 'notice')
    Assert-QP ((Test-QPInteger $Value['format'] 1 1) -and (Test-QPInteger $Value['noticeVersion'] 1 1)) '授权记录格式或告知版本无效。[QP_PRIVILEGE]'
    foreach ($pair in @(@('mode', 'system'), @('identity', 'LocalSystem'), @('acceptedBy', 'Administrator'), @('notice', 'full-system-management'))) {
        Assert-QP ($Value[$pair[0]] -is [string] -and $Value[$pair[0]] -ceq $pair[1]) '授权记录固定字段不正确。[QP_PRIVILEGE]'
    }
    Assert-QP ($Value['acceptedAt'] -is [string] -and $Value['acceptedAt'] -cmatch '\A[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\z') '授权时间必须是 UTC 秒格式，不接受偏移或小数秒。[QP_PRIVILEGE]'
    $time = Read-QPUtcTime $Value['acceptedAt']
    Assert-QP ($time -le [datetimeoffset]::UtcNow.AddMinutes(5)) '授权记录时间在未来。[QP_PRIVILEGE]'
    return ,$Value
}

function Test-QPInteractiveInput { return (-not [Console]::IsInputRedirected) }

function Confirm-QPSystemManagement {
    param([bool]$AcceptSystemManagement, [bool]$Yes)
    [Console]::WriteLine('最高权限告知：QQON-Panel 本体将以 LocalSystem（Windows 系统最高权限）运行，可管理整台服务器、安装软件、修改文件和服务。账户或面板漏洞可能导致整机被控制。此授权不同于安装确认、PATH 同意和普通 -Yes；Windows 暂不支持的软件功能仍会明确报告不支持。')
    if (-not $AcceptSystemManagement) {
        if ($Yes -or -not (Test-QPInteractiveInput)) { [Console]::WriteLine('未单独授权最高权限，保持 restricted 受限模式。'); return $null }
        if ((Read-QPConsole '只有输入完整“同意最高权限”才授权；其它输入保持受限模式：') -cne '同意最高权限') { return $null }
    }
    return Read-QPPrivilegeConsent @{ format = 1; mode = 'system'; identity = 'LocalSystem'; noticeVersion = 1; acceptedAt = [datetime]::UtcNow.ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture); acceptedBy = 'Administrator'; notice = 'full-system-management' }
}

function Get-QPConsentPath {
    param([hashtable]$Paths)
    # Paths comes only from Get-QPPaths in production, never a CLI/dataDir/env override.
    return [IO.Path]::Combine($Paths.State, 'management-consent.json')
}

function Read-QPManagementConsent {
    param([hashtable]$Paths, [switch]$AllowMissing, [switch]$Snapshot)
    $path = Get-QPConsentPath $Paths
    Assert-QPPlainPath $path -AllowMissing
    Assert-QP (-not [IO.Directory]::Exists($path)) '授权记录必须是固定普通文件。[QP_PRIVILEGE]'
    if (-not [IO.File]::Exists($path)) { Assert-QP ([bool]$AllowMissing) '缺少固定最高权限授权记录。[QP_PRIVILEGE]'; return $null }
    if (-not $Snapshot) { Assert-QPAcl $Paths.State 'Admin' -Root }
    Assert-QPAcl $path 'Admin' -File
    Assert-QP ([IO.File]::GetAccessControl($path).AreAccessRulesProtected) '授权记录必须使用 protected ACL。[QP_PRIVILEGE]'
    return Read-QPPrivilegeConsent (ConvertFrom-QPJson (Read-QPFile $path 4096 -Regular) 4096)
}

function Assert-QPSystemConsent {
    param([hashtable]$Paths, [hashtable]$Config)
    if ((Get-QPPrivilegeMode $Config) -cne 'system') { return }
    $record = Read-QPManagementConsent $Paths
    Assert-QP (Test-QPSameFields $Config['privilegeConsent'] $record @('format', 'mode', 'identity', 'noticeVersion', 'acceptedAt', 'acceptedBy', 'notice')) '安装配置与固定授权记录不一致。[QP_PRIVILEGE]'
}

function Write-QPManagementConsent {
    param([hashtable]$Paths, [hashtable]$Consent)
    [void](Read-QPPrivilegeConsent $Consent)
    Assert-QPAcl $Paths.State 'Admin' -Root
    $path = Get-QPConsentPath $Paths
    Write-QPAtomicJson $path $Consent
    [void](Read-QPManagementConsent $Paths)
}

function Read-QPConfigValue {
    param([hashtable]$Value, [switch]$AllowLegacy)
    $fields = @('format', 'repository', 'service', 'listen', 'origin', 'healthHost', 'version', 'dataSchema', 'commit', 'buildTime', 'binarySize', 'binarySHA256', 'installerSHA256', 'updaterSHA256')
    $legacy = (Test-QPInteger $Value['format'] 1 1)
    Assert-QP ((Test-QPInteger $Value['format'] 2 3) -or ($AllowLegacy -and $legacy)) '安装配置必须为 format2/format3；旧版需独立确认迁移，不能自动接管。'
    if (-not $legacy) { $fields += @('accessMode', 'tls') }
    if ($Value['format'] -eq 3) { $fields += @('privilegeMode', 'privilegeConsent') }
    Assert-QPFields $Value $fields
    if ($Value['format'] -eq 3) {
        if ((Get-QPPrivilegeMode $Value) -ceq 'system') { [void](Read-QPPrivilegeConsent $Value['privilegeConsent']) }
        else { Assert-QP ($null -eq $Value['privilegeConsent']) '受限模式不得声明最高权限同意。[QP_PRIVILEGE]' }
    }
    Assert-QPIdentity $Value
    if ((Get-QPPrivilegeMode $Value) -ceq 'system') { Assert-QP ((ConvertTo-QPVersion $Value['version']) -ge [version]'0.7.0') '最高权限模式要求 v0.7.0 或更新程序。[QP_PRIVILEGE]' }
    Assert-QP ($Value['service'] -is [string] -and $Value['service'] -ceq 'QQONPanel') 'Windows 服务身份不正确。'
    Assert-QP ($Value['listen'] -is [string] -and $Value['listen'] -cmatch '\A(127\.0\.0\.1|0\.0\.0\.0):([1-9][0-9]{3,4})\z') '监听地址只能是固定 IPv4 回环或所有 IPv4 接口及规范端口。'
    $port = [int]($Value['listen'].Split([char]':')[1])
    Assert-QP ($Value['origin'] -is [string] -and $Value['healthHost'] -is [string]) '访问地址或健康检查主机类型不正确。'
    $mode = $Value['accessMode']
    if ($legacy) { $mode = 'local'; if ($Value['origin']) { $mode = 'proxy' } }
    Assert-QP ($mode -is [string] -and $mode -cin @('local', 'proxy', 'public-selfsigned', 'public-certificate')) '安装配置访问模式无效。'
    if ($mode -cin @('local', 'proxy')) {
        $info = Get-QPAccess $mode $port '' $Value['origin']
        Assert-QP ($legacy -or $null -eq $Value['tls']) '本地或代理模式不得保存 TLS 代次。'
    } else {
        Assert-QPFields $Value['tls'] @('generation', 'host', 'fingerprint', 'notAfter')
        $tls = $Value['tls']
        Assert-QP ($tls['generation'] -is [string] -and $tls['generation'] -cmatch '\A[0-9a-f]{32}\z' -and $tls['fingerprint'] -is [string] -and $tls['fingerprint'] -cmatch '\A[0-9a-f]{64}\z') '证书代次或指纹无效；不允许保存任意证书路径。'
        Assert-QP ($tls['host'] -is [string] -and $tls['notAfter'] -is [string]) '证书主机和到期时间必须是字符串。'
        [void](Read-QPUtcTime $tls['notAfter'])
        $info = Get-QPAccess $mode $port $tls['host']
        Assert-QP ($info.host -ceq $tls['host']) '证书主机必须采用规范格式。'
    }
    foreach ($key in @('listen', 'origin', 'healthHost')) { Assert-QP ($info[$key] -ceq $Value[$key]) '访问模式、监听地址、HTTPS 地址和健康检查主机不一致。' }
    Assert-QP (Test-QPInteger $Value['binarySize'] 1 67108864) '已安装程序大小无效。'
    foreach ($key in @('binarySHA256', 'installerSHA256', 'updaterSHA256')) { Assert-QP ($Value[$key] -is [string] -and $Value[$key] -cmatch '\A[0-9a-f]{64}\z') '安装摘要无效。' }
    return ,$Value
}

function Read-QPConfig {
    param([hashtable]$Paths, [switch]$AllowLegacy)
    Assert-QPAcl $Paths.Config 'Admin' -File
    return Read-QPConfigValue (ConvertFrom-QPJson (Read-QPFile $Paths.Config 32768)) -AllowLegacy:$AllowLegacy
}

function Assert-QPKnownLegacy {
    param([hashtable]$Config)
    [void](Read-QPConfigValue $Config -AllowLegacy)
    $v05 = $Config['format'] -eq 1 -and $Config['installerSHA256'] -ceq 'db0bf026d4debb409fd14b30f0837cc7ba4d5a3f48cfec121545feff8c3931c8' -and $Config['updaterSHA256'] -ceq '383e0eec3c969ed9dd93ee8e05c1ebc4fd2d12bb4162dda6300025cfe951278d'
    $v06 = $Config['format'] -eq 2 -and $Config['installerSHA256'] -ceq 'eb21039d21cd5aa29b4c75ae8eb14c1424ef76e08c89a233dd05c0c30c648168' -and $Config['updaterSHA256'] -ceq '84a262f5aa6305d1053ad043976fa6bc544bfec5ba3d5ca3632f33d889a7f133'
    Assert-QP ($v05 -or $v06) '仅支持已知官方 v0.5/v0.6 管理器迁移；配置内任意 hash 不能作为迁移信任锚点。'
}

function Test-QPSameFields {
    param([hashtable]$Left, [hashtable]$Right, [string[]]$Fields)
    foreach ($key in $Fields) {
        if ($key -cin @('tls', 'privilegeConsent')) {
            if (($null -eq $Left[$key]) -ne ($null -eq $Right[$key])) { return $false }
            $nested = @('generation', 'host', 'fingerprint', 'notAfter')
            if ($key -ceq 'privilegeConsent') { $nested = @('format', 'mode', 'identity', 'noticeVersion', 'acceptedAt', 'acceptedBy', 'notice') }
            if ($null -ne $Left[$key] -and -not (Test-QPSameFields $Left[$key] $Right[$key] $nested)) { return $false }
        } elseif ($Left[$key] -cne $Right[$key]) { return $false }
    }
    return $true
}

function Read-QPTransaction {
    param([hashtable]$Paths)
    Assert-QPAcl $Paths.Journal 'Admin' -File
    $value = ConvertFrom-QPJson (Read-QPFile $Paths.Journal 32768)
    $legacy = (Test-QPInteger $value['format'] 1 1)
    Assert-QP ($legacy -or (Test-QPInteger $value['format'] 2 3)) '事务日志格式不受支持。'
    Assert-QP ($value['kind'] -is [string] -and $value['kind'] -cin @('install', 'update', 'config', 'renew', 'backup', 'migrate') -and $value['phase'] -is [string] -and $value['phase'] -cin @('prepared', 'ready')) '事务类型或阶段不受支持。'
    $kind = $value['kind']
    $fields = @('format', 'kind', 'phase', 'old', 'new')
    if ($kind -cin @('config', 'renew', 'backup', 'migrate')) { $fields += 'wasRunning'; Assert-QP (-not $legacy -and $value['wasRunning'] -is [bool]) '维护事务必须使用 format2 并记录原服务状态。' }
    if ($kind -cin @('backup', 'migrate')) { $fields += 'backup'; Assert-QP ($value['backup'] -is [string] -and $value['backup'] -cmatch '\A[0-9a-f]{32}\z') '事务备份只允许固定目录下的代次名称。' }
    Assert-QPFields $value $fields
    if ($kind -ceq 'backup') {
        Assert-QP ($null -eq $value['new']) '备份事务不能包含要应用的新配置。'
        [void](Read-QPConfigValue $value['old'])
        return ,$value
    }
    $new = Read-QPConfigValue $value['new'] -AllowLegacy:$legacy
    Assert-QP ($new['format'] -eq $value['format']) '事务格式与新配置格式不一致。'
    if ($kind -ceq 'install') { Assert-QP ($null -eq $value['old']) '新安装事务不能包含旧状态。' }
    else {
        $old = Read-QPConfigValue $value['old'] -AllowLegacy:($legacy -or $kind -ceq 'migrate')
        if ($kind -ceq 'migrate') {
            Assert-QPKnownLegacy $old
            Assert-QP (Test-QPSameFields $old $new @('repository', 'service', 'listen', 'origin', 'healthHost', 'version', 'commit', 'buildTime', 'dataSchema', 'binarySize', 'binarySHA256')) '管理器迁移不得修改应用、账户或原有访问方式。'
            if ($old['format'] -eq 1) { Assert-QP ($null -eq $new['tls'] -and $new['accessMode'] -cin @('local', 'proxy')) '旧版迁移不能直接启用公网 TLS。' }
            else { Assert-QP (Test-QPSameFields $old $new @('accessMode', 'tls')) '管理器迁移不得改变原证书或访问方式。' }
            Assert-QP ((Get-QPPrivilegeMode $new) -ceq 'restricted' -and $null -eq $new['privilegeConsent']) '管理器迁移不是最高权限授权。[QP_PRIVILEGE]'
        } elseif ($kind -ceq 'update') {
            $fixed = @('format', 'repository', 'service', 'listen', 'origin', 'healthHost', 'dataSchema', 'installerSHA256', 'updaterSHA256')
            if (-not $legacy) { $fixed += @('accessMode', 'tls', 'privilegeMode', 'privilegeConsent') }
            Assert-QP (Test-QPSameFields $old $new $fixed) '升级事务修改了固定访问设置或管理器。'
            Assert-QP ((ConvertTo-QPVersion $new['version']) -gt (ConvertTo-QPVersion $old['version']) -and $old['binarySHA256'] -cne $new['binarySHA256']) '事务不是严格的版本及程序摘要升级。'
        } else {
            Assert-QP (Test-QPSameFields $old $new @('format', 'repository', 'service', 'version', 'commit', 'buildTime', 'dataSchema', 'binarySize', 'binarySHA256', 'installerSHA256', 'updaterSHA256', 'privilegeMode', 'privilegeConsent')) '访问配置事务不能修改应用、管理器、权限或数据格式。'
            if ($kind -ceq 'renew') { Assert-QP ($old['accessMode'] -ceq 'public-selfsigned' -and $new['accessMode'] -ceq 'public-selfsigned' -and (Test-QPSameFields $old $new @('listen', 'origin', 'healthHost'))) '续期仅允许原自签访问地址的新证书代次。' }
            if ($null -ne $new['tls'] -and $null -ne $old['tls']) { Assert-QP ($new['tls']['generation'] -cne $old['tls']['generation']) '证书变更必须使用全新代次，禁止覆盖旧证书。' }
        }
        if ($legacy) { Assert-QPKnownLegacy $old }
    }
    if ($legacy) { Assert-QPKnownLegacy $new }
    return ,$value
}

function Enter-QPLock {
    $security = [Security.AccessControl.MutexSecurity]::new()
    $security.SetAccessRuleProtection($true, $false)
    $security.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
    foreach ($sid in @('S-1-5-18', 'S-1-5-32-544')) {
        $security.AddAccessRule([Security.AccessControl.MutexAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid), [Security.AccessControl.MutexRights]::FullControl, [Security.AccessControl.AccessControlType]::Allow))
    }
    $created = $false
    $mutex = [Threading.Mutex]::new($false, 'Global\QQONPanel.Setup.Format3', [ref]$created, $security)
    try {
        $actual = $mutex.GetAccessControl()
        Assert-QP ($actual.GetOwner([Security.Principal.SecurityIdentifier]).Value -in @('S-1-5-18', 'S-1-5-32-544') -and $actual.AreAccessRulesProtected) 'Unsafe preexisting setup mutex.'
        foreach ($rule in $actual.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
            Assert-QP ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $rule.IdentityReference.Value -in @('S-1-5-18', 'S-1-5-32-544')) 'Unsafe setup mutex access.'
        }
        $acquired = $false
        try { $acquired = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $acquired = $true }
        Assert-QP $acquired 'Another QQON-Panel installation/update is running.'
        return $mutex
    } catch { $mutex.Dispose(); throw }
}

function Exit-QPLock {
    param([Threading.Mutex]$Mutex)
    try { $Mutex.ReleaseMutex() } finally { $Mutex.Dispose() }
}

function Get-QPService {
    $items = @(Get-CimInstance -ClassName Win32_Service -Filter "Name='QQONPanel'" -OperationTimeoutSec 10 -ErrorAction Stop)
    Assert-QP ($items.Count -le 1) 'Ambiguous QQONPanel service identity.'
    if ($items.Count -eq 0) { return $null }
    return $items[0]
}

function Get-QPTlsPaths {
    param([hashtable]$Paths, [string]$Generation)
    Assert-QP ($Generation -cmatch '\A[0-9a-f]{32}\z') 'TLS 代次必须是 32 位小写十六进制值。'
    $root = [IO.Path]::Combine($Paths.State, 'tls')
    $directory = [IO.Path]::Combine($root, $Generation)
    return @{ Root = $root; Directory = $directory; Certificate = [IO.Path]::Combine($directory, 'cert.pem'); Key = [IO.Path]::Combine($directory, 'key.pem') }
}

function Read-QPPemPair {
    param([string]$Certificate, [string]$PrivateKey)
    # Source paths exist only in this call, never in install.json or any journal.
    $certBytes = Read-QPFile $Certificate 1048576 -Regular
    $keyBytes = Read-QPFile $PrivateKey 65536 -Regular
    $ascii = [Text.Encoding]::ASCII
    Assert-QP ($ascii.GetString($certBytes) -match '\A\s*-----BEGIN CERTIFICATE-----[\s\S]+-----END CERTIFICATE-----\s*\z') '导入证书必须是有界 PEM 证书链普通文件。'
    Assert-QP ($ascii.GetString($keyBytes) -match '\A\s*-----BEGIN (?:EC |RSA )?PRIVATE KEY-----[\s\S]+-----END (?:EC |RSA )?PRIVATE KEY-----\s*\z') '导入密钥必须是有界、未加密 PEM 普通文件；不会保存来源路径或显示内容。'
    return @{ Certificate = $certBytes; Key = $keyBytes }
}

function Assert-QPTlsInfo {
    param([byte[]]$Bytes, [string]$Name, [hashtable]$Files)
    $info = ConvertFrom-QPJson $Bytes 32768
    Assert-QPFields $info @('host', 'fingerprint', 'notAfter', 'certFile', 'keyFile')
    Assert-QP ($info['host'] -is [string] -and $info['host'] -ceq $Name -and $info['fingerprint'] -is [string] -and $info['fingerprint'] -cmatch '\A[0-9a-f]{64}\z') '证书 CLI 返回的主机或 SHA-256 指纹无效。'
    Assert-QP ($info['certFile'] -is [string] -and $info['keyFile'] -is [string] -and $info['certFile'] -ceq $Files.Certificate -and $info['keyFile'] -ceq $Files.Key) '证书 CLI 返回了非固定证书文件路径。'
    Assert-QP ($info['notAfter'] -is [string] -and (Read-QPUtcTime $info['notAfter']) -gt [datetimeoffset]::UtcNow) '证书已过期或 CLI 到期时间无效。'
    return ,$info
}

function New-QPTlsGeneration {
    param([hashtable]$Paths, [hashtable]$Config, [hashtable]$Access, [string]$Binary, [string]$Certificate = '', [string]$PrivateKey = '')
    Assert-QP ((ConvertTo-QPVersion $Config['version']) -ge [version]'0.6.0') '公网 TLS 需要应用 v0.6.0 或以上；请先 qqon update，旧程序不能执行证书 CLI。'
    Assert-QPBinary $Binary $Config
    $generation = [guid]::NewGuid().ToString('N'); $files = Get-QPTlsPaths $Paths $generation
    $kind = Get-QPStorageKind $Config 'Tls'
    if (-not [IO.Directory]::Exists($files.Root)) { New-QPPrivateDirectory $files.Root $kind }
    Assert-QPAcl $files.Root $kind -Root
    Assert-QPPlainPath $files.Directory -AllowMissing
    Assert-QP (-not [IO.Directory]::Exists($files.Directory) -and -not [IO.File]::Exists($files.Directory)) '证书代次必须全新；禁止覆盖旧目录。'
    if ($Access.accessMode -ceq 'public-selfsigned') {
        Assert-QP (-not $Certificate -and -not $PrivateKey) '自签模式不能提供导入文件。'
        $raw = Invoke-QPProcess $Binary @('-tls-create', '-tls-host', $Access.host, '-tls-output', $files.Directory, '-tls-days', '365') 30000
        [void](Assert-QPTlsInfo $raw $Access.host $files)
    } else {
        Assert-QP ($Access.accessMode -ceq 'public-certificate' -and $Certificate -and $PrivateKey) '证书导入模式必须提供证书和密钥普通文件。'
        $pair = Read-QPPemPair $Certificate $PrivateKey
        New-QPPrivateDirectory $files.Directory 'Admin'
        Write-QPNewFile $files.Certificate $pair.Certificate 'Admin'
        Write-QPNewFile $files.Key $pair.Key 'Admin'
        [Array]::Clear($pair.Key, 0, $pair.Key.Length)
    }
    Assert-QPPlainPath $files.Directory
    $entries = @([IO.Directory]::EnumerateFileSystemEntries($files.Directory))
    Assert-QP ($entries.Count -eq 2 -and @($entries | Where-Object { [IO.Path]::GetFileName($_) -cnotin @('cert.pem', 'key.pem') }).Count -eq 0) '证书代次必须只包含 cert.pem 和 key.pem。'
    foreach ($path in @($files.Certificate, $files.Key)) { Assert-QPPlainPath $path; Assert-QP ([IO.File]::Exists($path)) '证书和密钥必须是普通文件。' }
    # Main creates a current-administrator-private directory; inspect before granting the service read access.
    $info = Assert-QPTlsInfo (Invoke-QPProcess $Binary @('-tls-inspect', '-tls-host', $Access.host, '-tls-cert', $files.Certificate, '-tls-key', $files.Key) 15000) $Access.host $files
    [IO.Directory]::SetAccessControl($files.Directory, (New-QPSecurity $kind))
    foreach ($path in @($files.Certificate, $files.Key)) { [IO.File]::SetAccessControl($path, (New-QPSecurity $kind -File)); Assert-QPAcl $path $kind -File }
    Assert-QPAcl $files.Directory $kind -Root
    return @{ generation = $generation; host = $info['host']; fingerprint = $info['fingerprint']; notAfter = $info['notAfter'] }
}

function Assert-QPActiveTls {
    param([hashtable]$Paths, [hashtable]$Config, [switch]$Inspect)
    if ($null -eq $Config['tls']) { return }
    $files = Get-QPTlsPaths $Paths $Config['tls']['generation']; $kind = Get-QPStorageKind $Config 'Tls'
    Assert-QPAcl $files.Root $kind -Root; Assert-QPAcl $files.Directory $kind -Root
    Assert-QPAcl $files.Certificate $kind -File; Assert-QPAcl $files.Key $kind -File
    if ($Inspect) {
        $info = Assert-QPTlsInfo (Invoke-QPProcess $Paths.Binary @('-tls-inspect', '-tls-host', $Config['tls']['host'], '-tls-cert', $files.Certificate, '-tls-key', $files.Key) 15000) $Config['tls']['host'] $files
        Assert-QP ($info['fingerprint'] -ceq $Config['tls']['fingerprint'] -and $info['notAfter'] -ceq $Config['tls']['notAfter']) '活动证书与安装记录的固定指纹或有效期不一致。'
    }
}

function Initialize-QPHttpsProbe {
    if ($null -ne ('QQONPanel.PinnedHttps' -as [type])) { return }
    # Per-connection SslStream callback only: no OS trust store or global TLS callback changes.
    Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Diagnostics;
namespace QQONPanel {
  public static class PinnedHttps {
    static int Length(byte[] b, ref int p, int end) {
      if (p >= end) throw new IOException("SAN 长度无效。");
      int n = b[p++]; if (n < 128) return n;
      int c = n & 127; if (c == 0 || c > 4 || p + c > end) throw new IOException("SAN 长度无效。");
      n = 0; for (int i = 0; i < c; i++) n = checked(n * 256 + b[p++]); return n;
    }
    static bool HostMatches(X509Certificate2 cert, string host) {
      IPAddress ip; bool isIP = IPAddress.TryParse(host, out ip);
      foreach (X509Extension ext in cert.Extensions) {
        if (ext.Oid.Value != "2.5.29.17") continue;
        byte[] b = ext.RawData; int p = 0;
        if (b.Length < 2 || b[p++] != 48) return false;
        int end = checked(p + 1); int n = Length(b, ref p, b.Length); end = checked(p + n);
        if (end != b.Length) return false;
        bool matched = false;
        while (p < end) {
          int tag = b[p++], size = Length(b, ref p, end);
          if (size < 0 || size > end - p) return false;
          if (isIP && tag == 135 && size == 4 && ip.AddressFamily == AddressFamily.InterNetwork) {
            byte[] wanted = ip.GetAddressBytes(); bool same = true;
            for (int i = 0; i < 4; i++) if (b[p+i] != wanted[i]) same = false;
            if (same) matched = true;
          }
          if (!isIP && tag == 130) {
            string dns = Encoding.ASCII.GetString(b, p, size).ToLowerInvariant();
            if (dns == host) matched = true;
            if (dns.StartsWith("*.", StringComparison.Ordinal) && host.IndexOf('.') > 0 && host.Substring(host.IndexOf('.')) == dns.Substring(1)) matched = true;
          }
          p += size;
        }
        return matched;
      }
      return false; // CN-only certificates are not SAN verification.
    }
    public static bool Validate(X509Certificate2 cert, string host, string pin, DateTime notAfter, DateTime now) {
      try {
        if (cert == null || now < cert.NotBefore.ToUniversalTime() || now >= cert.NotAfter.ToUniversalTime() || cert.NotAfter.ToUniversalTime() != notAfter.ToUniversalTime()) return false;
        string actual;
        using (SHA256 sha = SHA256.Create()) actual = BitConverter.ToString(sha.ComputeHash(cert.RawData)).Replace("-", "").ToLowerInvariant();
        if (!String.Equals(actual, pin, StringComparison.Ordinal) || !HostMatches(cert, host)) return false;
        foreach (X509Extension ext in cert.Extensions) if (ext.Oid.Value == "2.5.29.37") {
          var eku = new X509EnhancedKeyUsageExtension(ext, ext.Critical); bool ok = false;
          foreach (Oid oid in eku.EnhancedKeyUsages) if (oid.Value == "1.3.6.1.5.5.7.3.1" || oid.Value == "2.5.29.37.0") ok = true;
          if (!ok) return false;
        }
        return true;
      } catch { return false; }
    }
    public static byte[] Probe(int port, string host, string authority, string pin, DateTime expiry) {
      using (TcpClient tcp = new TcpClient(AddressFamily.InterNetwork)) {
        if (!tcp.ConnectAsync(IPAddress.Loopback, port).Wait(2000)) throw new IOException("回环 HTTPS 连接超时。");
        tcp.ReceiveTimeout = 2000; tcp.SendTimeout = 2000;
        RemoteCertificateValidationCallback validate = (sender, remote, chain, errors) => {
          if (remote == null || (errors & SslPolicyErrors.RemoteCertificateNotAvailable) != 0) return false;
          using (var cert = new X509Certificate2(remote)) return Validate(cert, host, pin, expiry, DateTime.UtcNow);
        };
        using (SslStream tls = new SslStream(tcp.GetStream(), false, validate)) {
          // The trust anchor is the exact leaf pin. SAN, validity and EKU are checked independently above.
          if (!tls.AuthenticateAsClientAsync(host, null, SslProtocols.Tls12, false).Wait(2000)) throw new IOException("回环 TLS 握手超时。");
          tls.ReadTimeout = 2000; tls.WriteTimeout = 2000;
          byte[] request = Encoding.ASCII.GetBytes("GET /healthz HTTP/1.1\r\nHost: " + authority + "\r\nConnection: close\r\nAccept-Encoding: identity\r\n\r\n");
          tls.Write(request, 0, request.Length);
          using (MemoryStream data = new MemoryStream()) {
            byte[] buffer = new byte[1024]; int n; Stopwatch clock = Stopwatch.StartNew();
            while ((n = tls.Read(buffer, 0, buffer.Length)) != 0) {
              if (data.Length + n > 12288 || clock.ElapsedMilliseconds >= 2000) throw new IOException("HTTPS 健康响应超限或超时。");
              data.Write(buffer, 0, n); tls.ReadTimeout = Math.Max(1, 2000 - (int)clock.ElapsedMilliseconds);
            }
            byte[] raw = data.ToArray(); string text = Encoding.ASCII.GetString(raw); int split = text.IndexOf("\r\n\r\n", StringComparison.Ordinal);
            if (split < 0 || split > 8192 || raw.Length - split - 4 > 4096) throw new IOException("HTTPS 健康响应格式无效。");
            string header = text.Substring(0, split);
            if (!header.StartsWith("HTTP/1.1 200 ", StringComparison.Ordinal) && !header.StartsWith("HTTP/1.0 200 ", StringComparison.Ordinal)) throw new IOException("HTTPS 健康响应状态不是 200。");
            foreach (string line in header.Split(new string[]{"\r\n"}, StringSplitOptions.None)) {
              if (line.StartsWith("Transfer-Encoding:", StringComparison.OrdinalIgnoreCase)) throw new IOException("不支持编码健康响应。");
              if (line.StartsWith("Content-Encoding:", StringComparison.OrdinalIgnoreCase) && line.Substring(17).Trim() != "identity") throw new IOException("不支持压缩健康响应。");
              if (line.StartsWith("Content-Length:", StringComparison.OrdinalIgnoreCase)) { int length; if (!Int32.TryParse(line.Substring(15).Trim(), out length) || length != raw.Length - split - 4) throw new IOException("健康响应长度不一致。"); }
            }
            byte[] body = new byte[raw.Length - split - 4]; Buffer.BlockCopy(raw, split + 4, body, 0, body.Length); return body;
          }
        }
      }
    }
  }
}
'@ -ErrorAction Stop
}

function Get-QPServiceCommand {
    param([hashtable]$Paths, [hashtable]$Config)
    [void](Read-QPConfigValue $Config -AllowLegacy)
    $command = (ConvertTo-QPArgument $Paths.Binary) + ' -listen ' + (ConvertTo-QPArgument $Config['listen']) + ' -data-dir ' + (ConvertTo-QPArgument $Paths.Data)
    if ($Config['origin']) { $command += ' -origin ' + (ConvertTo-QPArgument $Config['origin']) }
    if ($null -ne $Config['tls']) {
        $tls = Get-QPTlsPaths $Paths $Config['tls']['generation']
        $command += ' -tls-cert ' + (ConvertTo-QPArgument $tls.Certificate) + ' -tls-key ' + (ConvertTo-QPArgument $tls.Key)
    }
    return $command + ' -github-repo LinyuFiee/QQON-Panel'
}

function Get-QPServiceIdentity {
    param([hashtable]$Config)
    if ((Get-QPPrivilegeMode $Config) -ceq 'system') { return 'LocalSystem' }
    return 'NT SERVICE\QQONPanel'
}

function Assert-QPServiceDefinition {
    param([hashtable]$Paths, [hashtable]$Config, $Service)
    Assert-QP ($null -ne $Service) 'Managed QQONPanel service is missing; manual installations are not adopted.'
    Assert-QP ($Service.Name -ceq 'QQONPanel' -and $Service.DisplayName -ceq 'QQON-Panel' -and $Service.StartName -ieq (Get-QPServiceIdentity $Config) -and $Service.ServiceType -eq 'Own Process' -and $Service.StartMode -eq 'Auto' -and $Service.PathName -ceq (Get-QPServiceCommand $Paths $Config)) 'Service account, startup type, SCM executable or arguments do not match managed metadata.'
}

function Assert-QPService {
    param([hashtable]$Paths, [hashtable]$Config, $Service)
    Assert-QPServiceDefinition $Paths $Config $Service
    $key = [Microsoft.Win32.Registry]::LocalMachine.OpenSubKey('SYSTEM\CurrentControlSet\Services\QQONPanel', $false)
    try {
        Assert-QP ($null -ne $key -and $key.GetValue('ServiceSidType', 0) -eq 1 -and $key.GetValue('Type', 0) -eq 16 -and $key.GetValue('Start', -1) -eq 2) 'Service must be own-process SCM, automatic, with unrestricted service SID.'
        Assert-QP ($null -eq $key.GetValue('FailureCommand') -and $null -eq $key.GetValue('Environment') -and $null -eq $key.GetValue('DependOnService')) 'Unsupported service hooks/environment/dependencies.'
    } finally { if ($null -ne $key) { $key.Dispose() } }
}

function Register-QPService {
    param([hashtable]$Paths, [hashtable]$Config)
    Assert-QP ($null -eq (Get-QPService)) 'QQONPanel service already exists; refusing to overwrite it.'
    $sc = [IO.Path]::Combine([Environment]::SystemDirectory, 'sc.exe')
    Assert-QPSystemConsent $Paths $Config
    [void](Invoke-QPProcess $sc @('create', 'QQONPanel', 'binPath=', (Get-QPServiceCommand $Paths $Config), 'type=', 'own', 'start=', 'auto', 'error=', 'normal', 'obj=', (Get-QPServiceIdentity $Config), 'DisplayName=', 'QQON-Panel'))
    [void](Invoke-QPProcess $sc @('sidtype', 'QQONPanel', 'unrestricted'))
    if ((Get-QPPrivilegeMode $Config) -ceq 'restricted') {
        $translated = [Security.Principal.NTAccount]::new('NT SERVICE\QQONPanel').Translate([Security.Principal.SecurityIdentifier])
        Assert-QP ($translated.Value -ceq (Get-QPServiceSid).Value) 'Windows virtual-service SID does not match the expected identity.'
    }
    Assert-QPService $Paths $Config (Get-QPService)
}

function Get-QPBinaryProcesses {
    param([string]$Binary)
    $base = [IO.Path]::GetFileNameWithoutExtension($Binary)
    $result = [Collections.Generic.List[int]]::new()
    foreach ($process in [Diagnostics.Process]::GetProcessesByName($base)) {
        try {
            $path = $process.MainModule.FileName
            if ([string]::Equals($path, $Binary, [StringComparison]::OrdinalIgnoreCase)) { [void]$result.Add($process.Id) }
        } catch { throw '无法确认 panel.exe 的进程身份，拒绝切换程序文件。' }
        finally { $process.Dispose() }
    }
    return $result.ToArray()
}

function Test-QPFileUnlocked {
    param([string]$Path)
    $stream = $null
    try {
        $stream = [IO.FileStream]::new($Path, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        return $true
    } catch [IO.IOException] { return $false }
    finally { if ($null -ne $stream) { $stream.Dispose() } }
}

function New-QPServiceController {
    Add-Type -AssemblyName System.ServiceProcess -ErrorAction Stop
    return [ServiceProcess.ServiceController]::new('QQONPanel')
}

function Stop-QPServiceAndWait {
    param([hashtable]$Paths, [hashtable]$Config)
    $service = Get-QPService; Assert-QPService $Paths $Config $service
    $originalPid = [int]$service.ProcessId
    $controller = New-QPServiceController
    try {
        $controller.Refresh()
        if ($controller.Status -ne [ServiceProcess.ServiceControllerStatus]::Stopped -and $controller.Status -ne [ServiceProcess.ServiceControllerStatus]::StopPending) { $controller.Stop() }
        $originalPidGone = $originalPid -le 0
        $clock = [Diagnostics.Stopwatch]::StartNew()
        while ($clock.Elapsed.TotalSeconds -lt 60) {
            $controller.Refresh()
            if (-not $originalPidGone) {
                try {
                    $process = [Diagnostics.Process]::GetProcessById($originalPid)
                    try { $originalPidGone = $process.HasExited } finally { $process.Dispose() }
                } catch [ArgumentException] { $originalPidGone = $true }
            }
            if ($controller.Status -eq [ServiceProcess.ServiceControllerStatus]::Stopped -and $originalPidGone -and @(Get-QPBinaryProcesses $Paths.Binary).Count -eq 0 -and (Test-QPFileUnlocked $Paths.Binary)) { return }
            Start-Sleep -Milliseconds 200
        }
        throw '服务、进程或文件锁未在 60 秒内停止或释放；禁止强制结束进程或删除锁文件绕过。'
    } finally { $controller.Dispose() }
}

function Start-QPService {
    param([hashtable]$Paths, [hashtable]$Config)
    Assert-QPSystemConsent $Paths $Config
    Assert-QPService $Paths $Config (Get-QPService)
    $controller = New-QPServiceController
    try {
        $controller.Refresh()
        if ($controller.Status -eq [ServiceProcess.ServiceControllerStatus]::Running) { return }
        Assert-QP ($controller.Status -eq [ServiceProcess.ServiceControllerStatus]::Stopped) 'Service is in an unexpected transitional state.'
        $controller.Start()
        $controller.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Running, [timespan]::FromSeconds(30))
    } finally { $controller.Dispose() }
}

function Test-QPHealth {
    param([hashtable]$Config)
    if ($null -ne $Config['tls']) {
        try {
            Initialize-QPHttpsProbe
            $port = [int]($Config['listen'].Split([char]':')[1])
            $expiry = (Read-QPUtcTime $Config['tls']['notAfter']).UtcDateTime
            $raw = [QQONPanel.PinnedHttps]::Probe($port, $Config['tls']['host'], $Config['healthHost'], $Config['tls']['fingerprint'], $expiry)
            $value = ConvertFrom-QPJson $raw 4096
            Assert-QPFields $value @('status', 'version', 'dataSchema')
            return ($value['status'] -ceq 'ok' -and $value['version'] -ceq $Config['version'] -and (Test-QPInteger $value['dataSchema'] 1 1))
        } catch { return $false }
    }
    $request = [Net.HttpWebRequest]::CreateHttp('http://' + $Config['listen'] + '/healthz')
    $request.Proxy = $null; $request.AllowAutoRedirect = $false; $request.Host = $Config['healthHost']
    $request.Timeout = 2000; $request.ReadWriteTimeout = 2000; $request.KeepAlive = $false
    $request.Headers['Accept-Encoding'] = 'identity'
    $response = $null
    try {
        $response = $request.GetResponse()
        if ([int]$response.StatusCode -ne 200 -or $response.ContentLength -gt 4096 -or (-not [string]::IsNullOrEmpty($response.ContentEncoding) -and $response.ContentEncoding -cne 'identity')) { return $false }
        $stream = $response.GetResponseStream(); $memory = [IO.MemoryStream]::new(); $buffer = New-Object byte[] 1024
        $clock = [Diagnostics.Stopwatch]::StartNew()
        try {
            while ($true) {
                if ($clock.ElapsedMilliseconds -ge 2000) { return $false }
                if ($stream.CanTimeout) { $stream.ReadTimeout = [Math]::Max(1, 2000 - [int]$clock.ElapsedMilliseconds) }
                $n = $stream.Read($buffer, 0, [Math]::Min(1024, 4097 - [int]$memory.Length))
                if ($n -eq 0) { break }; if ($memory.Length + $n -gt 4096) { return $false }; $memory.Write($buffer, 0, $n)
            }
            $value = ConvertFrom-QPJson $memory.ToArray() 4096
            Assert-QPFields $value @('status', 'version', 'dataSchema')
            return ($value['status'] -is [string] -and $value['status'] -ceq 'ok' -and $value['version'] -is [string] -and $value['version'] -ceq $Config['version'] -and (Test-QPInteger $value['dataSchema'] 1 1))
        } finally { $stream.Dispose(); $memory.Dispose() }
    } catch { return $false }
    finally { if ($null -ne $response) { $response.Dispose() }; $request.Abort() }
}

function Wait-QPHealthy {
    param([hashtable]$Paths, [hashtable]$Config)
    $clock = [Diagnostics.Stopwatch]::StartNew()
    while ($clock.Elapsed.TotalSeconds -lt 30) {
        $service = Get-QPService
        Assert-QPService $Paths $Config $service
        if ($service.State -ceq 'Running' -and [int]$service.ProcessId -gt 0 -and (Test-QPHealth $Config)) { return }
        Start-Sleep -Milliseconds 250
    }
    throw 'QQONPanel 未在 30 秒内通过精确版本、数据格式、主机及证书健康检查。'
}

function Assert-QPLayout {
    param([hashtable]$Paths)
    $config = @{}
    if ([IO.File]::Exists($Paths.Config)) { $config = Read-QPConfig $Paths -AllowLegacy }
    $codeKind = Get-QPStorageKind $config 'Code'; $stateKind = Get-QPStorageKind $config 'State'; $dataKind = Get-QPStorageKind $config 'Data'
    Assert-QPAcl $Paths.Code $codeKind -Root
    Assert-QPAcl $Paths.State $stateKind -Root
    Assert-QPAcl $Paths.Data $dataKind -Root
    $allowed = @('panel.exe', 'panel.previous.exe', 'panel.next.exe', 'install.ps1', 'update.ps1', 'qqon.ps1', 'qqon.cmd')
    foreach ($entry in [IO.Directory]::EnumerateFileSystemEntries($Paths.Code)) {
        Assert-QP ([IO.Path]::GetFileName($entry) -cin $allowed) 'Unknown file in managed code directory; no automatic adoption or deletion.'
        Assert-QPAcl $entry $codeKind -File
    }
    # Validate nested account/TOTP/logs/jobs as well as the data root.
    $pending = [Collections.Generic.Stack[string]]::new()
    $pending.Push($Paths.Data); $count = 0; $clock = [Diagnostics.Stopwatch]::StartNew()
    while ($pending.Count -gt 0) {
        $parent = $pending.Pop()
        foreach ($entry in [IO.Directory]::EnumerateFileSystemEntries($parent)) {
            $count++; Assert-QP ($count -le 100000 -and $clock.Elapsed.TotalSeconds -lt 30) 'Private data validation limit exceeded; refusing without changing ACLs.'
            Assert-QPPlainPath $entry
            $isDirectory = ([IO.File]::GetAttributes($entry) -band [IO.FileAttributes]::Directory) -ne 0
            Assert-QPAcl $entry $dataKind -File:(-not $isDirectory)
            if ($isDirectory) { $pending.Push($entry) }
        }
    }
    # Read/validate only; data contents are never removed or rolled back.
}

function Assert-QPManagers {
    param([hashtable]$Paths, [hashtable]$Config)
    Assert-QPAcl $Paths.Installer 'Code' -File; Assert-QPAcl $Paths.Updater 'Code' -File
    Assert-QP ((Get-QPFileHash $Paths.Installer 524288) -ceq $Config['installerSHA256'] -and (Get-QPFileHash $Paths.Updater 32768) -ceq $Config['updaterSHA256']) 'Installed setup manager/wrapper was modified; refusing automatic adoption.'
}

function Set-QPServiceConfig {
    param([hashtable]$Paths, [hashtable]$Before, [hashtable]$After)
    Assert-QPService $Paths $Before (Get-QPService)
    [void](Read-QPConfigValue $After -AllowLegacy)
    Assert-QP ((Get-QPPrivilegeMode $Before) -ceq (Get-QPPrivilegeMode $After)) '普通服务配置事务不能改变权限身份。[QP_PRIVILEGE]'
    $sc = [IO.Path]::Combine([Environment]::SystemDirectory, 'sc.exe')
    [void](Invoke-QPProcess $sc @('config', 'QQONPanel', 'binPath=', (Get-QPServiceCommand $Paths $After)))
    Assert-QPService $Paths $After (Get-QPService)
}

function Get-QPTransactionServiceConfig {
    param([hashtable]$Paths, [hashtable]$Transaction)
    $service = Get-QPService
    Assert-QP ($null -ne $service) '恢复所需的 QQONPanel 服务不存在。'
    foreach ($config in @($Transaction['old'], $Transaction['new'])) {
        if ($null -ne $config -and $service.PathName -ceq (Get-QPServiceCommand $Paths $config) -and $service.StartName -ieq (Get-QPServiceIdentity $config)) { Assert-QPService $Paths $config $service; return ,$config }
    }
    throw 'SCM 命令既不匹配旧配置也不匹配新配置；拒绝修改未知服务。'
}

function Restore-QPOriginalService {
    param([hashtable]$Paths, [hashtable]$Config, [bool]$WasRunning)
    if ($WasRunning) { Start-QPService $Paths $Config; Wait-QPHealthy $Paths $Config }
    else { Stop-QPServiceAndWait $Paths $Config }
}

function Undo-QPConfigTransaction {
    param([hashtable]$Paths, [hashtable]$Transaction)
    $actual = Get-QPTransactionServiceConfig $Paths $Transaction
    Stop-QPServiceAndWait $Paths $actual
    Assert-QPActiveTls $Paths $Transaction['old']
    Set-QPServiceConfig $Paths $actual $Transaction['old']
    Write-QPAtomicJson $Paths.Config $Transaction['old']
    Restore-QPOriginalService $Paths $Transaction['old'] $Transaction['wasRunning']
    Remove-QPManagedFile $Paths.Journal 'Admin'
}

function Invoke-QPConfigTransaction {
    param([hashtable]$Paths, [hashtable]$Old, [hashtable]$New, [string]$Kind = 'config')
    Assert-QP (-not [IO.File]::Exists($Paths.Journal)) '已有未完成事务，请先确认恢复。'
    $service = Get-QPService; Assert-QPService $Paths $Old $service
    Assert-QP ($service.State -cin @('Running', 'Stopped')) '服务正处于过渡状态，请稍后重试。'
    $transaction = @{ format = $New['format']; kind = $Kind; phase = 'prepared'; old = $Old; new = $New; wasRunning = ($service.State -ceq 'Running') }
    Assert-QPActiveTls $Paths $New -Inspect
    Write-QPAtomicJson $Paths.Journal $transaction
    try {
        [void](Read-QPTransaction $Paths)
        Stop-QPServiceAndWait $Paths $Old
        Set-QPServiceConfig $Paths $Old $New
        Write-QPAtomicJson $Paths.Config $New
        $transaction['phase'] = 'ready'; Write-QPAtomicJson $Paths.Journal $transaction
        Start-QPService $Paths $New
        Wait-QPHealthy $Paths $New
        if (-not $transaction['wasRunning']) { Stop-QPServiceAndWait $Paths $New }
        Remove-QPManagedFile $Paths.Journal 'Admin'
    } catch {
        try { Undo-QPConfigTransaction $Paths $transaction }
        catch { throw '访问配置变更失败，回滚未完成；事务日志及新旧证书已保留，请修复后重新确认恢复。账户数据未重置。' }
        throw '访问配置变更失败，已恢复旧配置、SCM 命令及原服务状态；账户数据未重置，新旧证书代次均保留。'
    }
}

function Get-QPPrivilegeJournalPath {
    param([hashtable]$Paths)
    return [IO.Path]::Combine($Paths.State, 'privilege-transaction.json')
}

function Get-QPPrivilegeBackupPath {
    param([hashtable]$Paths, [string]$Generation)
    Assert-QP ($Generation -cmatch '\A[0-9a-f]{32}\z') '权限备份代次无效。[QP_PRIVILEGE]'
    return [IO.Path]::Combine($Paths.State, 'privilege-backups', $Generation)
}

function Enter-QPPrivilegeSecurity {
    # RestorePrivilege is needed to restore the original virtual-account owner.
    # Scope affects only this process token and restores its previous setting.
    if ($null -eq ('QQONPanel.RestorePrivilegeScope' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace QQONPanel {
  public sealed class RestorePrivilegeScope : IDisposable {
    [StructLayout(LayoutKind.Sequential)] struct Luid { public uint Low; public int High; }
    [StructLayout(LayoutKind.Sequential)] struct Privileges { public uint Count; public Luid Id; public uint Attributes; }
    [DllImport("advapi32.dll", SetLastError=true)] static extern bool OpenProcessToken(IntPtr process, uint access, out IntPtr token);
    [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool LookupPrivilegeValue(string system, string name, out Luid value);
    [DllImport("advapi32.dll", SetLastError=true)] static extern bool AdjustTokenPrivileges(IntPtr token, bool disable, ref Privileges value, uint size, out Privileges previous, out uint length);
    [DllImport("kernel32.dll")] static extern IntPtr GetCurrentProcess();
    [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
    IntPtr token; Privileges previous;
    public RestorePrivilegeScope() {
      if (!OpenProcessToken(GetCurrentProcess(), 0x28, out token)) throw new Win32Exception();
      try {
        Luid id; if (!LookupPrivilegeValue(null, "SeRestorePrivilege", out id)) throw new Win32Exception();
        Privileges value = new Privileges { Count=1, Id=id, Attributes=2 }; uint length;
        if (!AdjustTokenPrivileges(token, false, ref value, (uint)Marshal.SizeOf(typeof(Privileges)), out previous, out length) || Marshal.GetLastWin32Error()!=0) throw new Win32Exception();
      } catch { CloseHandle(token); token=IntPtr.Zero; throw; }
    }
    public void Dispose() {
      if (token==IntPtr.Zero) return;
      Privileges ignored; uint length;
      try { if (!AdjustTokenPrivileges(token, false, ref previous, (uint)Marshal.SizeOf(typeof(Privileges)), out ignored, out length)) throw new Win32Exception(); }
      finally { CloseHandle(token); token=IntPtr.Zero; }
    }
  }
}
'@ -ErrorAction Stop
    }
    return [QQONPanel.RestorePrivilegeScope]::new()
}

function Get-QPPrivilegeEntryPath {
    param([hashtable]$Paths, [hashtable]$Entry)
    Assert-QP ($Entry['scope'] -is [string] -and $Entry['scope'] -cin @('code', 'state', 'data', 'tls') -and $Entry['relative'] -is [string] -and $Entry['directory'] -is [bool]) '权限快照路径字段无效。[QP_PRIVILEGE]'
    $relative = $Entry['relative']; $root = $Paths.State
    switch ($Entry['scope']) { 'code' { $root = $Paths.Code }; 'data' { $root = $Paths.Data }; 'tls' { $root = [IO.Path]::Combine($Paths.State, 'tls') } }
    if (-not $relative) { Assert-QP $Entry['directory'] '权限根条目必须为目录。[QP_PRIVILEGE]'; return $root }
    Assert-QP ($relative.Length -le 240 -and $relative -cnotmatch '[/:\x00-\x1f]' -and -not $relative.StartsWith('\')) '权限快照仅允许根内相对路径。[QP_PRIVILEGE]'
    foreach ($part in $relative.Split([char]'\')) { Assert-QP ($part -notin @('', '.', '..') -and $part -notmatch '[ .]\z') '权限快照路径不规范。[QP_PRIVILEGE]' }
    if ($Entry['scope'] -ceq 'state') { Assert-QP (-not $Entry['directory'] -and $relative -cin @('install.json', 'management-consent.json')) '拒绝权限事务以外的状态文件。[QP_PRIVILEGE]' }
    if ($Entry['scope'] -ceq 'code') { Assert-QP (-not $Entry['directory'] -and $relative -cin @('panel.exe', 'panel.previous.exe', 'panel.next.exe', 'install.ps1', 'update.ps1', 'qqon.ps1', 'qqon.cmd')) '拒绝未知程序目录条目。[QP_PRIVILEGE]' }
    if ($Entry['scope'] -ceq 'tls') { Assert-QP (($Entry['directory'] -and $relative -cmatch '\A[0-9a-f]{32}\z') -or (-not $Entry['directory'] -and $relative -cmatch '\A[0-9a-f]{32}\\(?:cert|key)\.pem\z')) '拒绝未知 TLS 代次条目。[QP_PRIVILEGE]' }
    $path = [IO.Path]::Combine($root, $relative); Assert-QPPlainPath $path -AllowMissing
    return $path
}

function Get-QPPrivilegeEntries {
    param([hashtable]$Paths)
    $result = [Collections.Generic.List[object]]::new(); $pending = [Collections.Generic.Queue[object]]::new()
    foreach ($scope in @('code', 'state', 'data', 'tls')) {
        $entry = @{ scope = $scope; relative = ''; directory = $true }
        $path = Get-QPPrivilegeEntryPath $Paths $entry
        if ($scope -ceq 'tls' -and -not [IO.Directory]::Exists($path)) { Assert-QPPlainPath $path -AllowMissing; Assert-QP (-not [IO.File]::Exists($path)) 'TLS 根不能是文件。'; continue }
        $pending.Enqueue($entry)
    }
    $clock = [Diagnostics.Stopwatch]::StartNew()
    while ($pending.Count -gt 0) {
        $entry = $pending.Dequeue(); $path = Get-QPPrivilegeEntryPath $Paths $entry
        Assert-QPPlainPath $path
        Assert-QP ($result.Count -lt 10000 -and $clock.Elapsed.TotalSeconds -lt 120) '权限快照超出 10000 条目或 120 秒限制。[QP_PRIVILEGE]'
        $actualDirectory = ([IO.File]::GetAttributes($path) -band [IO.FileAttributes]::Directory) -ne 0
        Assert-QP ($actualDirectory -eq $entry['directory']) '权限快照文件类型发生变化。[QP_PRIVILEGE]'
        [void]$result.Add($entry)
        if (-not $entry['directory']) { continue }
        if ($entry['scope'] -ceq 'state') {
            foreach ($name in @('install.json', 'management-consent.json')) {
                $file = [IO.Path]::Combine($path, $name); Assert-QPPlainPath $file -AllowMissing
                Assert-QP (-not [IO.Directory]::Exists($file)) '固定状态文件不能是目录。[QP_PRIVILEGE]'
                if ([IO.File]::Exists($file)) { $pending.Enqueue(@{ scope = 'state'; relative = $name; directory = $false }) }
            }
        } else {
            foreach ($child in [IO.Directory]::EnumerateFileSystemEntries($path)) {
                Assert-QP ($pending.Count + $result.Count -lt 10000) '权限快照条目过多。[QP_PRIVILEGE]'
                Assert-QPPlainPath $child
                $relative = [IO.Path]::GetFileName($child)
                if ($entry['relative']) { $relative = $entry['relative'] + '\' + $relative }
                $pending.Enqueue(@{ scope = $entry['scope']; relative = $relative; directory = (([IO.File]::GetAttributes($child) -band [IO.FileAttributes]::Directory) -ne 0) })
            }
        }
    }
    return ,($result.ToArray())
}

function Get-QPEntryKind {
    param([hashtable]$Entry)
    switch ($Entry['scope']) { 'code' { return 'Code' }; 'data' { return 'Data' }; 'tls' { return 'Tls' }; default { if ($Entry['directory']) { return 'State' }; return 'Admin' } }
}

function Test-QPSameSecurityDescriptor {
    param([string]$Expected, [string]$Actual)
    $left = [Security.AccessControl.RawSecurityDescriptor]::new($Expected); $right = [Security.AccessControl.RawSecurityDescriptor]::new($Actual)
    if ($left.Owner.Value -cne $right.Owner.Value -or $left.Group.Value -cne $right.Group.Value) { return $false }
    $protected = [Security.AccessControl.ControlFlags]::DiscretionaryAclProtected
    if (($left.ControlFlags -band $protected) -ne ($right.ControlFlags -band $protected)) { return $false }
    # Windows may add SE_DACL_AUTO_INHERITED when persisting an otherwise exact
    # DACL. Compare every ACE byte and the protection flag, not that bookkeeping bit.
    $a = New-Object byte[] $left.DiscretionaryAcl.BinaryLength; $b = New-Object byte[] $right.DiscretionaryAcl.BinaryLength
    $left.DiscretionaryAcl.GetBinaryForm($a, 0); $right.DiscretionaryAcl.GetBinaryForm($b, 0)
    return [Convert]::ToBase64String($a) -ceq [Convert]::ToBase64String($b)
}

function Set-QPEntrySecurity {
    param([string]$Path, [bool]$Directory, [string]$Sddl = '', [string]$Kind = 'Admin')
    Assert-QPPlainPath $Path
    $acl = New-QPSecurity $Kind -File:(-not $Directory)
    if ($Sddl) { $acl.SetSecurityDescriptorSddlForm($Sddl, [Security.AccessControl.AccessControlSections]'Owner, Group, Access') }
    if ($Directory) { [IO.Directory]::SetAccessControl($Path, $acl) }
    else {
        Initialize-QPProcessRunner; $guard = [QQONPanel.NativeProcess]::OpenRegularRead($Path)
        try { [IO.File]::SetAccessControl($Path, $acl) } finally { $guard.Dispose() }
    }
    if ($Sddl) {
        if ($Directory) { $actual = [IO.Directory]::GetAccessControl($Path) } else { $actual = [IO.File]::GetAccessControl($Path) }
        Assert-QP (Test-QPSameSecurityDescriptor $Sddl ($actual.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]'Owner, Group, Access'))) '恢复后 owner/group/DACL 或保护标记不匹配。[QP_PRIVILEGE]'
    }
}

function New-QPPrivilegeSnapshot {
    param([hashtable]$Paths, [hashtable]$Transaction)
    $backup = Get-QPPrivilegeBackupPath $Paths $Transaction['backup']; $root = [IO.Path]::GetDirectoryName($backup)
    if (-not [IO.Directory]::Exists($root)) { New-QPPrivateDirectory $root 'Admin' }
    Assert-QPAcl $root 'Admin' -Root; New-QPPrivateDirectory $backup 'Admin'
    $entries = Get-QPPrivilegeEntries $Paths; $budget = @{ Count = 0; Size = [long]0 }; $index = 0
    foreach ($entry in $entries) {
        $path = Get-QPPrivilegeEntryPath $Paths $entry; $kind = Get-QPEntryKind $entry
        Assert-QPAcl $path $kind -File:(-not $entry['directory']) -Root:($entry['directory'] -and -not $entry['relative'])
        if ($entry['directory']) { $acl = [IO.Directory]::GetAccessControl($path) } else { $acl = [IO.File]::GetAccessControl($path) }
        $entry['sddl'] = $acl.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]'Owner, Group, Access')
        Assert-QP ($entry['sddl'].Length -le 8192) '权限快照 ACL 过长。[QP_PRIVILEGE]'
        $entry['blob'] = $null; $entry['sha256'] = $null
        if (-not $entry['directory']) {
            $entry['blob'] = $index.ToString('D5', [Globalization.CultureInfo]::InvariantCulture) + '.bin'; $index++
            $target = [IO.Path]::Combine($backup, $entry['blob'])
            Copy-QPBackupFile $path $target $budget $kind
            $entry['sha256'] = Get-QPPrivilegeFileHash $target
        }
    }
    $record = @{ format = 1; entries = $entries; serviceIdentity = Get-QPServiceIdentity $Transaction['old']; serviceCommand = Get-QPServiceCommand $Paths $Transaction['old'] }
    $bytes = ConvertTo-QPJsonBytes $record
    Assert-QP ($bytes.Length -le 16777216) '权限快照元数据超过 16 MiB。[QP_PRIVILEGE]'
    Write-QPNewFile ([IO.Path]::Combine($backup, 'snapshot.json')) $bytes 'Admin'
    return Get-QPSha256 $bytes
}

function Get-QPPrivilegeFileHash {
    param([string]$Path)
    Initialize-QPProcessRunner; $stream = [QQONPanel.NativeProcess]::OpenRegularRead($Path); $sha = [Security.Cryptography.SHA256]::Create()
    try { Assert-QP ($stream.Length -le 536870912) '权限快照文件超限。'; return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() }
    finally { $sha.Dispose(); $stream.Dispose() }
}

function Read-QPPrivilegeTransaction {
    param([hashtable]$Paths)
    $journal = Get-QPPrivilegeJournalPath $Paths; Assert-QPAcl $journal 'Admin' -File
    $value = ConvertFrom-QPJson (Read-QPFile $journal 32768 -Regular)
    Assert-QPFields $value @('format', 'kind', 'phase', 'old', 'new', 'wasRunning', 'backup', 'snapshotSHA256', 'oldConsent')
    Assert-QP ((Test-QPInteger $value['format'] 1 1) -and $value['kind'] -ceq 'privilege' -and $value['phase'] -cin @('prepared', 'backed-up', 'ready') -and $value['wasRunning'] -is [bool]) '权限日志格式无效。[QP_PRIVILEGE]'
    [void](Get-QPPrivilegeBackupPath $Paths $value['backup'])
    $old = Read-QPConfigValue $value['old']; $new = Read-QPConfigValue $value['new']
    Assert-QP ($old['format'] -eq 3 -and $new['format'] -eq 3 -and (Get-QPPrivilegeMode $old) -ceq 'restricted' -and (Get-QPPrivilegeMode $new) -ceq 'system') '权限日志只能执行独立受限到系统模式迁移。[QP_PRIVILEGE]'
    $fixed = @($old.Keys | Where-Object { $_ -cnotin @('privilegeMode', 'privilegeConsent') })
    Assert-QP (Test-QPSameFields $old $new $fixed) '权限事务不能改变应用、访问方式或管理器。[QP_PRIVILEGE]'
    if ($null -ne $value['oldConsent']) { [void](Read-QPPrivilegeConsent $value['oldConsent']) }
    if ($value['phase'] -ceq 'prepared') { Assert-QP ($null -eq $value['snapshotSHA256']) '未备份阶段不可声明完成快照。[QP_PRIVILEGE]' }
    else { Assert-QP ($value['snapshotSHA256'] -is [string] -and $value['snapshotSHA256'] -cmatch '\A[0-9a-f]{64}\z') '权限快照摘要无效。[QP_PRIVILEGE]' }
    return ,$value
}

function Assert-QPPrivilegeDescriptor {
    param([hashtable]$Entry)
    Assert-QP ($Entry['sddl'] -is [string] -and $Entry['sddl'].Length -le 8192) '权限快照缺少原 owner/DACL。[QP_PRIVILEGE]'
    $descriptor = [Security.AccessControl.RawSecurityDescriptor]::new($Entry['sddl'])
    $owners = @('S-1-5-18', 'S-1-5-32-544'); if ($Entry['scope'] -ceq 'data') { $owners += (Get-QPServiceSid).Value }
    Assert-QP ($null -ne $descriptor.Owner -and $descriptor.Owner.Value -cin $owners -and $null -ne $descriptor.DiscretionaryAcl) '权限快照 owner/DACL 不可信。[QP_PRIVILEGE]'
    $rights = Get-QPRights (Get-QPEntryKind $Entry)
    foreach ($ace in $descriptor.DiscretionaryAcl) {
        Assert-QP ($ace -is [Security.AccessControl.CommonAce] -and $ace.AceQualifier -eq [Security.AccessControl.AceQualifier]::AccessAllowed -and -not $ace.IsCallback -and $rights.ContainsKey($ace.SecurityIdentifier.Value)) '权限快照含未知访问规则。[QP_PRIVILEGE]'
        $max = [long]$rights[$ace.SecurityIdentifier.Value] -bor [long][Security.AccessControl.FileSystemRights]::Synchronize
        Assert-QP (([long]$ace.AccessMask -band (-bnot $max)) -eq 0) '权限快照含过度访问规则。[QP_PRIVILEGE]'
    }
}

function Read-QPPrivilegeSnapshot {
    param([hashtable]$Paths, [hashtable]$Transaction)
    $backup = Get-QPPrivilegeBackupPath $Paths $Transaction['backup']; Assert-QPAcl ([IO.Path]::GetDirectoryName($backup)) 'Admin' -Root; Assert-QPAcl $backup 'Admin' -Root
    $path = [IO.Path]::Combine($backup, 'snapshot.json'); Assert-QPAcl $path 'Admin' -File
    $bytes = Read-QPFile $path 16777216 -Regular
    Assert-QP ((Get-QPSha256 $bytes) -ceq $Transaction['snapshotSHA256']) '权限快照摘要发生变化。[QP_PRIVILEGE]'
    $value = ConvertFrom-QPJson $bytes 16777216; Assert-QPFields $value @('format', 'entries', 'serviceIdentity', 'serviceCommand')
    Assert-QP ($value['format'] -eq 1 -and $value['entries'] -is [array] -and $value['entries'].Count -le 10000 -and $value['serviceIdentity'] -ceq (Get-QPServiceIdentity $Transaction['old']) -and $value['serviceCommand'] -ceq (Get-QPServiceCommand $Paths $Transaction['old'])) '权限快照服务定义无效。[QP_PRIVILEGE]'
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase); $blobs = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($entry in $value['entries']) {
        Assert-QPFields $entry @('scope', 'relative', 'directory', 'sddl', 'blob', 'sha256')
        $target = Get-QPPrivilegeEntryPath $Paths $entry; Assert-QP ($seen.Add($target)) '权限快照路径重复。[QP_PRIVILEGE]'
        Assert-QPPrivilegeDescriptor $entry
        if ($entry['directory']) { Assert-QP ($null -eq $entry['blob'] -and $null -eq $entry['sha256']) '目录不能引用文件内容。[QP_PRIVILEGE]' }
        else {
            Assert-QP ($entry['blob'] -is [string] -and $entry['blob'] -cmatch '\A[0-9]{5}\.bin\z' -and $blobs.Add($entry['blob']) -and $entry['sha256'] -cmatch '\A[0-9a-f]{64}\z') '权限备份文件引用无效。[QP_PRIVILEGE]'
            $file = [IO.Path]::Combine($backup, $entry['blob']); Assert-QPAcl $file 'Admin' -File
            Assert-QP ((Get-QPPrivilegeFileHash $file) -ceq $entry['sha256']) '权限备份文件摘要不一致。[QP_PRIVILEGE]'
        }
    }
    foreach ($required in @($Paths.Code, $Paths.State, $Paths.Data, $Paths.Config, $Paths.Installer, $Paths.Updater, $Paths.Binary)) { Assert-QP ($seen.Contains($required)) '权限快照缺少必要条目。[QP_PRIVILEGE]' }
    return ,$value
}

function Set-QPPrivilegeIdentity {
    param([hashtable]$Paths, [hashtable]$Before, [hashtable]$After)
    $service = Get-QPService; Assert-QPService $Paths $Before $service
    Assert-QP ($service.State -ceq 'Stopped') '切换权限身份前服务必须停止。[QP_PRIVILEGE]'
    Assert-QPSystemConsent $Paths $After
    $sc = [IO.Path]::Combine([Environment]::SystemDirectory, 'sc.exe')
    [void](Invoke-QPProcess $sc @('config', 'QQONPanel', 'obj=', (Get-QPServiceIdentity $After)))
    Assert-QPService $Paths $After (Get-QPService)
}

function Undo-QPPrivilege {
    param([hashtable]$Paths, [hashtable]$Transaction)
    $old = $Transaction['old']; $journal = Get-QPPrivilegeJournalPath $Paths
    $actual = Get-QPTransactionServiceConfig $Paths $Transaction
    Stop-QPServiceAndWait $Paths $actual
    if ($Transaction['phase'] -ceq 'prepared') {
        Assert-QPManagers $Paths $old
        Assert-QP (Test-QPSameFields $old (Read-QPConfig $Paths) @($old.Keys)) '备份未完成却修改了配置，拒绝恢复。[QP_PRIVILEGE]'
        Assert-QP ((Get-QPServiceIdentity $actual) -ceq (Get-QPServiceIdentity $old)) '备份前不应改变服务身份。[QP_PRIVILEGE]'
    } else {
        $snapshot = Read-QPPrivilegeSnapshot $Paths $Transaction; $backup = Get-QPPrivilegeBackupPath $Paths $Transaction['backup']
        if ((Get-QPPrivilegeMode $actual) -cne 'restricted') { Set-QPPrivilegeIdentity $Paths $actual $old }
        # Restore exact config/consent bytes. Runtime account, TOTP and log contents
        # are intentionally never rewound, including writes made by a health probe.
        foreach ($entry in $snapshot['entries']) {
            if ($entry['scope'] -ceq 'state' -and -not $entry['directory']) {
                $target = Get-QPPrivilegeEntryPath $Paths $entry; $source = [IO.Path]::Combine($backup, $entry['blob'])
                $bytes = Read-QPFile $source 32768 -Regular
                $temporary = $target + '.restore-' + $Transaction['backup']
                Assert-QPPlainPath $temporary -AllowMissing
                if ([IO.File]::Exists($temporary)) { Assert-QPAcl $temporary 'Admin' -File; Assert-QP ((Get-QPPrivilegeFileHash $temporary) -ceq $entry['sha256']) '拒绝未知恢复暂存文件。'; [IO.File]::Delete($temporary) }
                Write-QPNewFile $temporary $bytes 'Admin'
                if ([IO.File]::Exists($target)) { Assert-QPAcl $target 'Admin' -File; [IO.File]::Replace($temporary, $target, [NullString]::Value) } else { [IO.File]::Move($temporary, $target) }
            } elseif ($entry['scope'] -cin @('code', 'tls') -and -not $entry['directory']) {
                Assert-QP ((Get-QPPrivilegeFileHash (Get-QPPrivilegeEntryPath $Paths $entry)) -ceq $entry['sha256']) '权限事务期间程序或证书字节变化，拒绝覆盖未知文件。[QP_PRIVILEGE]'
            }
        }
        if ($null -eq $Transaction['oldConsent']) { Remove-QPManagedFile (Get-QPConsentPath $Paths) 'Admin' }
        # Parent-first restoration ensures inherited descendants end with their
        # exact original descriptor, including virtual-account owner and protection.
        $currentEntries = Get-QPPrivilegeEntries $Paths
        $known = @{}
        foreach ($entry in $snapshot['entries']) { $known[(Get-QPPrivilegeEntryPath $Paths $entry)] = $entry }
        foreach ($entry in $currentEntries) {
            $path = Get-QPPrivilegeEntryPath $Paths $entry
            if ($known.ContainsKey($path)) { Set-QPEntrySecurity $path $entry['directory'] $known[$path]['sddl'] }
            else {
                Assert-QP ($entry['scope'] -ceq 'data') '恢复时发现权限快照以外的非数据条目。[QP_PRIVILEGE]'
                Set-QPEntrySecurity $path $entry['directory'] '' 'Data'
            }
        }
        foreach ($entry in $snapshot['entries']) { Assert-QPPlainPath (Get-QPPrivilegeEntryPath $Paths $entry) }
        Assert-QPManagers $Paths $old; Assert-QPLayout $Paths; Assert-QPActiveTls $Paths $old
    }
    Restore-QPOriginalService $Paths $old $Transaction['wasRunning']
    Remove-QPManagedFile $journal 'Admin'
}

function Resume-QPPrivilege {
    param([hashtable]$Paths)
    $transaction = Read-QPPrivilegeTransaction $Paths
    $scope = Enter-QPPrivilegeSecurity
    try { Undo-QPPrivilege $Paths $transaction } finally { $scope.Dispose() }
    [Console]::WriteLine('已恢复中断前的受限身份、owner/DACL、配置和授权记录；账户/TOTP/日志未重置，独立敏感快照保留。重新授权必须再次运行 qqon privilege。')
}

function Invoke-QPPrivilege {
    param([hashtable]$Paths, [hashtable]$Old, $Consent)
    Assert-QP ($null -ne $Consent) '没有独立最高权限同意，未迁移服务。[QP_PRIVILEGE]'
    [void](Read-QPPrivilegeConsent $Consent); [void](Read-QPConfigValue $Old)
    Assert-QP ($Old['format'] -eq 3) '请先通过已校验 v0.7 安装器独立迁移管理器；旧管理器不能直接授权。[QP_PRIVILEGE]'
    if ((Get-QPPrivilegeMode $Old) -ceq 'system') { Assert-QPSystemConsent $Paths $Old; [Console]::WriteLine('已是独立授权的系统模式，未改写同意时间或权限。'); return }
    Assert-QP ((ConvertTo-QPVersion $Old['version']) -ge [version]'0.7.0') '请先 qqon update 升级应用至 v0.7.0 或以上，再单独授权。[QP_PRIVILEGE]'
    $journal = Get-QPPrivilegeJournalPath $Paths
    Assert-QP (-not [IO.File]::Exists($journal) -and -not [IO.File]::Exists($Paths.Journal)) '必须先独立恢复未完成事务。[QP_PRIVILEGE]'
    Assert-QPLayout $Paths; Assert-QPManagers $Paths $Old; Assert-QPShortcuts $Paths; Assert-QPBinary $Paths.Binary $Old; Assert-QPActiveTls $Paths $Old
    $service = Get-QPService; Assert-QPService $Paths $Old $service
    Assert-QP ($service.State -cin @('Running', 'Stopped')) '服务处于过渡状态，不能开始授权迁移。[QP_PRIVILEGE]'
    $new = $Old.Clone(); $new['privilegeMode'] = 'system'; $new['privilegeConsent'] = $Consent
    $transaction = @{ format = 1; kind = 'privilege'; phase = 'prepared'; old = $Old; new = $new; wasRunning = ($service.State -ceq 'Running'); backup = [guid]::NewGuid().ToString('N'); snapshotSHA256 = $null; oldConsent = (Read-QPManagementConsent $Paths -AllowMissing -Snapshot) }
    $scope = Enter-QPPrivilegeSecurity
    try {
        Write-QPAtomicJson $journal $transaction
        try {
            [void](Read-QPPrivilegeTransaction $Paths)
            Stop-QPServiceAndWait $Paths $Old
            $transaction['snapshotSHA256'] = New-QPPrivilegeSnapshot $Paths $transaction
            $transaction['phase'] = 'backed-up'; Write-QPAtomicJson $journal $transaction
            $snapshot = Read-QPPrivilegeSnapshot $Paths $transaction
            foreach ($entry in $snapshot['entries']) { Set-QPEntrySecurity (Get-QPPrivilegeEntryPath $Paths $entry) $entry['directory'] }
            Write-QPManagementConsent $Paths $Consent
            Set-QPPrivilegeIdentity $Paths $Old $new
            Write-QPAtomicJson $Paths.Config $new
            $transaction['phase'] = 'ready'; Write-QPAtomicJson $journal $transaction
            Assert-QPLayout $Paths; Assert-QPActiveTls $Paths $new
            Start-QPService $Paths $new; Wait-QPHealthy $Paths $new
            if (-not $transaction['wasRunning']) { Stop-QPServiceAndWait $Paths $new }
            Remove-QPManagedFile $journal 'Admin'
        } catch {
            try { Undo-QPPrivilege $Paths $transaction }
            catch { throw '最高权限迁移失败且回滚未完成；权限日志和独立快照已保留，禁止继续普通更新。修复后确认恢复，不会重置账户。[QP_PRIVILEGE]' }
            throw '最高权限迁移失败；已恢复旧 owner/DACL、服务身份、配置和原授权记录，账户/TOTP/日志未重置。[QP_PRIVILEGE]'
        }
    } finally { $scope.Dispose() }
    [Console]::WriteLine('最高权限已独立授权：LocalSystem；固定授权记录受 Admin/SYSTEM 保护。含账户/TOTP/日志/TLS/管理器的敏感快照：' + (Get-QPPrivilegeBackupPath $Paths $transaction['backup']))
}

function Get-QPBackupDirectory {
    param([hashtable]$Paths, [string]$Generation)
    Assert-QP ($Generation -cmatch '\A[0-9a-f]{32}\z') '备份代次格式不正确。'
    $root = [IO.Path]::Combine($Paths.State, 'backups')
    Assert-QP (-not $root.StartsWith($Paths.Data.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) '备份目标不能位于数据源内。'
    return [IO.Path]::Combine($root, $Generation)
}

function Copy-QPBackupFile {
    param([string]$Source, [string]$Destination, [hashtable]$Budget, [string]$Kind = 'Data')
    Assert-QPAcl $Source $Kind -File
    Assert-QPPlainPath $Source
    Assert-QP (([IO.File]::GetAttributes($Source) -band [IO.FileAttributes]'Directory, ReparsePoint, Device') -eq 0) '备份拒绝链接、目录及特殊文件。'
    Initialize-QPProcessRunner
    $input = [QQONPanel.NativeProcess]::OpenRegularRead($Source)
    $output = $null
    try {
        $Budget.Count++; $Budget.Size += $input.Length
        Assert-QP ($Budget.Count -le 10000 -and $Budget.Size -le 536870912) '备份超过 10000 个文件或 512 MiB 限制。'
        Assert-QPPlainPath $Destination -AllowMissing
        $output = [IO.FileStream]::new($Destination, [IO.FileMode]::CreateNew, [Security.AccessControl.FileSystemRights]::Write, [IO.FileShare]::None, 65536, [IO.FileOptions]::WriteThrough, (New-QPSecurity 'Admin' -File))
        $buffer = New-Object byte[] 65536; [long]$remaining = $input.Length
        while ($remaining -gt 0) {
            $n = $input.Read($buffer, 0, [int][Math]::Min($remaining, $buffer.Length))
            Assert-QP ($n -gt 0) '备份源在复制中发生变化。'
            $output.Write($buffer, 0, $n); $remaining -= $n
        }
        Assert-QP ($input.ReadByte() -eq -1) '备份源在复制中增长。'
        $output.Flush($true)
    } finally { if ($null -ne $output) { $output.Dispose() }; $input.Dispose() }
    Assert-QPAcl $Destination 'Admin' -File
}

function New-QPBackupSnapshot {
    param([hashtable]$Paths, [hashtable]$Config, [string]$Generation)
    $destination = Get-QPBackupDirectory $Paths $Generation
    $root = [IO.Path]::GetDirectoryName($destination)
    if (-not [IO.Directory]::Exists($root)) { New-QPPrivateDirectory $root 'Admin' }
    Assert-QPAcl $root 'Admin' -Root
    New-QPPrivateDirectory $destination 'Admin'
    $data = [IO.Path]::Combine($destination, 'data'); New-QPPrivateDirectory $data 'Admin'
    $budget = @{ Size = [long]0; Count = 0 }
    $pending = [Collections.Generic.Stack[object]]::new(); $pending.Push(@{ Source = $Paths.Data; Target = $data })
    $clock = [Diagnostics.Stopwatch]::StartNew()
    while ($pending.Count -gt 0) {
        $pair = $pending.Pop(); Assert-QPAcl $pair.Source 'Data'
        foreach ($entry in [IO.Directory]::EnumerateFileSystemEntries($pair.Source)) {
            Assert-QP ($clock.Elapsed.TotalSeconds -lt 120) '备份超过 120 秒安全时限。'
            Assert-QPPlainPath $entry
            $name = [IO.Path]::GetFileName($entry)
            if ($name -ceq '.panel.lock') { continue }
            $target = [IO.Path]::Combine($pair.Target, $name)
            if (([IO.File]::GetAttributes($entry) -band [IO.FileAttributes]::Directory) -ne 0) {
                $budget.Count++; Assert-QP ($budget.Count -le 10000) '备份文件和目录数量超过 10000。'
                Assert-QPAcl $entry 'Data'; New-QPPrivateDirectory $target 'Admin'; $pending.Push(@{ Source = $entry; Target = $target })
            } else { Copy-QPBackupFile $entry $target $budget 'Data' }
        }
    }
    Copy-QPBackupFile $Paths.Config ([IO.Path]::Combine($destination, 'install.json')) $budget 'Admin'
    foreach ($name in @('install.ps1', 'update.ps1', 'qqon.ps1', 'qqon.cmd')) {
        $source = [IO.Path]::Combine($Paths.Code, $name)
        if ([IO.File]::Exists($source)) { Copy-QPBackupFile $source ([IO.Path]::Combine($destination, $name)) $budget 'Code' }
    }
    if ($null -ne $Config['tls']) {
        Assert-QPActiveTls $Paths $Config
        $tls = Get-QPTlsPaths $Paths $Config['tls']['generation']
        $tlsBackup = [IO.Path]::Combine($destination, 'tls'); New-QPPrivateDirectory $tlsBackup 'Admin'
        Copy-QPBackupFile $tls.Certificate ([IO.Path]::Combine($tlsBackup, 'cert.pem')) $budget 'Tls'
        Copy-QPBackupFile $tls.Key ([IO.Path]::Combine($tlsBackup, 'key.pem')) $budget 'Tls'
    }
    $record = @{ format = 1; dataSchema = 1; bytes = $budget.Size; entries = $budget.Count; serviceCommand = Get-QPServiceCommand $Paths $Config; created = [datetime]::UtcNow.ToString("yyyy-MM-dd'T'HH:mm:ss'Z'") }
    Write-QPNewFile ([IO.Path]::Combine($destination, 'snapshot.json')) (ConvertTo-QPJsonBytes $record) 'Admin'
    return @{ Path = $destination; Size = $budget.Size; Count = $budget.Count }
}

function Invoke-QPBackup {
    param([hashtable]$Paths, [hashtable]$Config)
    Assert-QP (-not [IO.File]::Exists($Paths.Journal)) '备份前必须先恢复现有事务。'
    $service = Get-QPService; Assert-QPService $Paths $Config $service
    Assert-QP ($service.State -cin @('Running', 'Stopped')) '服务正处于过渡状态，不能开始备份。'
    $transaction = @{ format = 2; kind = 'backup'; phase = 'prepared'; old = $Config; new = $null; wasRunning = ($service.State -ceq 'Running'); backup = [guid]::NewGuid().ToString('N') }
    Write-QPAtomicJson $Paths.Journal $transaction
    $ok = $false; $restored = $false; $snapshot = $null
    try {
        Stop-QPServiceAndWait $Paths $Config
        $snapshot = New-QPBackupSnapshot $Paths $Config $transaction['backup']
        $transaction['phase'] = 'ready'; Write-QPAtomicJson $Paths.Journal $transaction
        $ok = $true
    } catch { $ok = $false }
    finally {
        try { Restore-QPOriginalService $Paths $Config $transaction['wasRunning']; $restored = $true; Remove-QPManagedFile $Paths.Journal 'Admin' }
        catch { $restored = $false }
    }
    $path = Get-QPBackupDirectory $Paths $transaction['backup']
    Assert-QP $restored ('备份操作后未能恢复原服务状态；事务日志已保留。敏感备份或部分文件路径：' + $path)
    Assert-QP $ok ('备份失败或磁盘写入失败；原服务状态已恢复，部分敏感文件只保留在：' + $path)
    [Console]::WriteLine(('备份完成：{0}；{1} 字节，{2} 个条目。原服务状态已恢复。' -f $snapshot.Path, $snapshot.Size, $snapshot.Count))
    [Console]::WriteLine('备份包含账户数据及活动 TLS 私钥，仅管理员可读。不会自动恢复旧认证或重置账户，请妥善保管。')
}

function Get-QPShortcutContent {
    param([hashtable]$Paths)
    $text = [Text.UTF8Encoding]::new($false, $true).GetString((Read-QPFile $Paths.Updater 32768)).TrimStart([char]0xfeff)
    $begin = $text.IndexOf('# BEGIN TRUSTED WRAPPER', [StringComparison]::Ordinal)
    $end = $text.IndexOf('# END TRUSTED WRAPPER', [StringComparison]::Ordinal)
    Assert-QP ($begin -ge 0 -and $end -gt $begin) '已验证更新器缺少固定快捷入口模板。'
    $body = $text.Substring($begin, $end - $begin)
    $script = "# QQON-Panel 中文维护入口，由已验证安装器生成。`r`n" + $body + @'

if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference = 'Stop'
    try { Invoke-QPInstalledManager @($args) }
    catch { [Console]::Error.WriteLine('QQON-Panel 维护未完成：' + $_.Exception.Message); exit 1 }
}
'@
    # CMD only forwards to its sibling script. All semantic arguments are whitelist-checked in PowerShell.
    $cmd = '@"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0qqon.ps1" %*' + "`r`n"
    return @{ Script = [byte[]]([byte[]](0xef, 0xbb, 0xbf) + [Text.UTF8Encoding]::new($false).GetBytes($script)); Command = [Text.Encoding]::ASCII.GetBytes($cmd) }
}

function Write-QPShortcuts {
    param([hashtable]$Paths)
    $content = Get-QPShortcutContent $Paths
    $scriptPath = [IO.Path]::Combine($Paths.Code, 'qqon.ps1'); $cmdPath = [IO.Path]::Combine($Paths.Code, 'qqon.cmd')
    foreach ($item in @(@{ Path = $scriptPath; Bytes = $content.Script }, @{ Path = $cmdPath; Bytes = $content.Command })) {
        if ([IO.File]::Exists($item.Path)) {
            Assert-QPAcl $item.Path 'Code' -File
            Assert-QP ((Get-QPFileHash $item.Path 32768) -ceq (Get-QPSha256 $item.Bytes)) '已存在的快捷入口与固定模板不一致，拒绝覆盖。'
        } else { Write-QPNewFile $item.Path $item.Bytes 'Code' }
    }
}

function Assert-QPShortcuts {
    param([hashtable]$Paths)
    $content = Get-QPShortcutContent $Paths
    foreach ($item in @(@{ Name = 'qqon.ps1'; Bytes = $content.Script }, @{ Name = 'qqon.cmd'; Bytes = $content.Command })) {
        $path = [IO.Path]::Combine($Paths.Code, $item.Name)
        Assert-QPAcl $path 'Code' -File
        Assert-QP ((Get-QPFileHash $path 32768) -ceq (Get-QPSha256 $item.Bytes)) '维护快捷入口已变化，拒绝执行或自动修复。'
    }
}

function Get-QPPathAppend {
    param([string]$Current, [string]$Code)
    Assert-QP ($Code -cmatch '\A[A-Za-z]:\\' -and $Code -notmatch '[;\r\n%]' -and $Code.EndsWith('\QQON-Panel', [StringComparison]::Ordinal)) '只能追加已验证的固定 QQON-Panel 程序目录。'
    Assert-QP ($Current.Length -le 30000 -and $Current -notmatch '[\x00\r\n]') '现有系统 PATH 无法安全追加，未进行修改。'
    foreach ($entry in $Current.Split([char]';')) { if ($entry.TrimEnd('\') -ieq $Code.TrimEnd('\')) { return @{ Changed = $false; Value = $Current } } }
    $separator = ';'; if ($Current.Length -eq 0 -or $Current.EndsWith(';')) { $separator = '' }
    return @{ Changed = $true; Value = $Current + $separator + $Code }
}

function Add-QPSystemPath {
    param([hashtable]$Paths)
    $fixed = Get-QPPaths
    Assert-QP ($Paths.Code -ceq $fixed.Code) 'PATH 只能追加当前系统的固定安装目录。'
    Assert-QPAcl $Paths.Code 'Code' -Root
    $registry = [Microsoft.Win32.Registry]::LocalMachine.OpenSubKey('SYSTEM\CurrentControlSet\Control\Session Manager\Environment', $true)
    try {
        Assert-QP ($null -ne $registry) '无法打开系统 PATH 配置；其他环境变量未修改。'
        $before = $registry.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        Assert-QP ($before -is [string]) '系统 PATH 类型不正确，拒绝修改。'
        $kind = $registry.GetValueKind('Path')
        Assert-QP ($kind -in @([Microsoft.Win32.RegistryValueKind]::String, [Microsoft.Win32.RegistryValueKind]::ExpandString)) '系统 PATH 类型不受支持。'
        $append = Get-QPPathAppend $before $Paths.Code
        if ($append.Changed) {
            Assert-QP ($registry.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) -ceq $before) '系统 PATH 在操作期间变化，请重试。'
            $registry.SetValue('Path', $append.Value, $kind)
        }
    } finally { if ($null -ne $registry) { $registry.Dispose() } }
    [Console]::WriteLine('已仅追加固定程序目录到系统 PATH；当前父终端不会更新，请重新打开终端（必要时重新登录）。')
}

function Get-QPMigrationStagePath {
    param([string]$Path, [string]$Generation)
    Assert-QP ($Generation -cmatch '\A[0-9a-f]{32}\z' -and [IO.Path]::GetFileName($Path) -cin @('install.ps1', 'update.ps1')) '迁移暂存路径只能由事务备份代次和固定管理器文件名推导。[QP_MIGRATION_STAGE]'
    return $Path + '.write-' + $Generation
}

function Remove-QPMigrationStaging {
    param([hashtable]$Paths, [hashtable]$Transaction)
    Assert-QP ($Transaction['format'] -in @(2, 3) -and $Transaction['kind'] -ceq 'migrate') '仅允许清理已验证迁移事务的暂存文件。[QP_MIGRATION_STAGE]'
    Assert-QPKnownLegacy $Transaction['old']
    Assert-QPAcl $Paths.Code 'Code' -Root; Assert-QPAcl $Paths.State 'State' -Root
    $targets = @(
        @{ Name = 'install.ps1'; Digest = 'installerSHA256'; Limit = 524288 }
        @{ Name = 'update.ps1'; Digest = 'updaterSHA256'; Limit = 32768 }
    )
    $staging = @{}
    foreach ($item in $targets) {
        $target = [IO.Path]::Combine($Paths.Code, $item.Name)
        Assert-QPAcl $target 'Code' -File
        $hash = Get-QPSha256 (Read-QPFile $target $item.Limit -Regular)
        Assert-QP ($hash -cin @($Transaction['old'][$item.Digest], $Transaction['new'][$item.Digest])) '暂存清理前，目标管理器必须匹配本次事务的新旧摘要。[QP_MIGRATION_STAGE]'
        $stage = Get-QPMigrationStagePath $target $Transaction['backup']
        $staging[[IO.Path]::GetFileName($stage)] = $item
    }
    $allowed = @('panel.exe', 'panel.previous.exe', 'panel.next.exe', 'install.ps1', 'update.ps1', 'qqon.ps1', 'qqon.cmd')
    $validated = [Collections.Generic.List[string]]::new()
    # Validate the entire fixed directory before deleting anything; no wildcard adoption.
    foreach ($entry in [IO.Directory]::EnumerateFileSystemEntries($Paths.Code)) {
        $name = [IO.Path]::GetFileName($entry)
        Assert-QPAcl $entry 'Code' -File
        if ($name -cin $allowed) { continue }
        Assert-QP ($name -cin @($staging.Keys)) '程序目录包含不属于本次迁移事务的文件，未清理任何暂存文件。[QP_MIGRATION_STAGE]'
        Assert-QP ([IO.File]::GetAccessControl($entry).AreAccessRulesProtected) '迁移暂存文件必须使用受保护 ACL。[QP_MIGRATION_STAGE]'
        $item = $staging[$name]
        $hash = Get-QPSha256 (Read-QPFile $entry $item.Limit -Regular)
        Assert-QP ($hash -cin @($Transaction['old'][$item.Digest], $Transaction['new'][$item.Digest])) '迁移暂存文件摘要不属于本次事务，未执行清理。[QP_MIGRATION_STAGE]'
        [void]$validated.Add($entry)
    }
    foreach ($entry in $validated) { Remove-QPManagedFile $entry 'Code' }
}

function Write-QPAtomicCode {
    param([string]$Path, [byte[]]$Bytes, [string]$Generation)
    Assert-QPAcl $Path 'Code' -File
    $temporary = Get-QPMigrationStagePath $Path $Generation
    Assert-QPPlainPath $temporary -AllowMissing
    Assert-QP (-not [IO.File]::Exists($temporary) -and -not [IO.Directory]::Exists($temporary)) '事务暂存路径已存在，请先通过已校验安装器恢复；不会覆盖或删除未知文件。[QP_MIGRATION_STAGE]'
    try { Write-QPNewFile $temporary $Bytes 'Code'; [IO.File]::Replace($temporary, $Path, [NullString]::Value) }
    finally { if ([IO.File]::Exists($temporary)) { Remove-QPManagedFile $temporary 'Code' } }
}

function Undo-QPMigration {
    param([hashtable]$Paths, [hashtable]$Transaction)
    $old = $Transaction['old']; Assert-QPKnownLegacy $old
    $backup = Get-QPBackupDirectory $Paths $Transaction['backup']
    Assert-QPAcl ([IO.Path]::GetDirectoryName($backup)) 'Admin' -Root; Assert-QPAcl $backup 'Admin' -Root
    $savedConfig = [IO.Path]::Combine($backup, 'install.json'); Assert-QPAcl $savedConfig 'Admin' -File
    $saved = Read-QPConfigValue (ConvertFrom-QPJson (Read-QPFile $savedConfig 32768)) -AllowLegacy
    Assert-QP (Test-QPSameFields $old $saved @($old.Keys)) '独立事务备份与迁移旧配置不一致。'
    Stop-QPServiceAndWait $Paths $old
    # Accept only a shortcut generated by the pinned old or selected new wrapper.
    # Old v0.6 shortcuts may be partially removed when a crash occurs.
    $oldShortcut = $null
    if ($old['format'] -eq 2) {
        $backupUpdater = [IO.Path]::Combine($backup, 'update.ps1')
        Assert-QPAcl $backupUpdater 'Admin' -File
        Assert-QP ((Get-QPFileHash $backupUpdater 32768) -ceq $old['updaterSHA256']) '旧快捷模板摘要不匹配。'
        $templatePaths = $Paths.Clone(); $templatePaths.Updater = $backupUpdater
        $oldShortcut = Get-QPShortcutContent $templatePaths
    }
    $currentHash = Get-QPFileHash $Paths.Updater 32768
    $generated = $null
    if ($currentHash -ceq $Transaction['new']['updaterSHA256']) { $generated = Get-QPShortcutContent $Paths }
    else { Assert-QP ($currentHash -ceq $old['updaterSHA256']) '当前更新器摘要不属于迁移事务。' }
    foreach ($item in @(@{ Name = 'qqon.ps1'; Key = 'Script' }, @{ Name = 'qqon.cmd'; Key = 'Command' })) {
        $path = [IO.Path]::Combine($Paths.Code, $item.Name)
        if (-not [IO.File]::Exists($path)) { continue }
        Assert-QPAcl $path 'Code' -File; $hash = Get-QPFileHash $path 32768
        $allowed = @()
        if ($null -ne $generated) { $allowed += Get-QPSha256 $generated[$item.Key] }
        if ($null -ne $oldShortcut) { $allowed += Get-QPSha256 $oldShortcut[$item.Key] }
        Assert-QP ($hash -cin $allowed) '迁移快捷入口已被修改，拒绝自动删除。'
        Remove-QPManagedFile $path 'Code'
    }
    foreach ($item in @(@{ Name = 'install.ps1'; Digest = 'installerSHA256' }, @{ Name = 'update.ps1'; Digest = 'updaterSHA256' })) {
        $source = [IO.Path]::Combine($backup, $item.Name); $target = [IO.Path]::Combine($Paths.Code, $item.Name)
        Assert-QPAcl $source 'Admin' -File; Assert-QPAcl $target 'Code' -File
        $bytes = Read-QPFile $source 524288
        Assert-QP ((Get-QPSha256 $bytes) -ceq $old[$item.Digest]) '迁移备份管理器不是已知官方旧版本。'
        Assert-QP ((Get-QPFileHash $target 524288) -cin @($old[$item.Digest], $Transaction['new'][$item.Digest])) '当前管理器不是本次事务的新旧版本，拒绝覆盖。'
        Write-QPAtomicCode $target $bytes $Transaction['backup']
    }
    if ($old['format'] -eq 2) { Write-QPShortcuts $Paths }
    Write-QPAtomicJson $Paths.Config $old
    Restore-QPOriginalService $Paths $old $Transaction['wasRunning']
    Remove-QPManagedFile $Paths.Journal 'Admin'
}

function Invoke-QPMigration {
    param([hashtable]$Paths, [string]$Version, [string]$Source)
    Assert-QPLayout $Paths
    Assert-QP (-not [IO.File]::Exists($Paths.Journal) -and -not [IO.File]::Exists((Get-QPPrivilegeJournalPath $Paths))) '旧版有未完成事务，请先恢复，不可同时迁移管理器。'
    $old = Read-QPConfig $Paths -AllowLegacy
    Assert-QPKnownLegacy $old; Assert-QPManagers $Paths $old; Assert-QPBinary $Paths.Binary $old
    if ($old['format'] -eq 1) {
        foreach ($name in @('qqon.ps1', 'qqon.cmd')) { Assert-QP (-not [IO.File]::Exists([IO.Path]::Combine($Paths.Code, $name))) '旧版目录已有未知快捷入口，拒绝自动迁移或覆盖。' }
    } else { Assert-QPShortcuts $Paths; Assert-QPActiveTls $Paths $old }
    $service = Get-QPService; Assert-QPService $Paths $old $service
    Assert-QP ($service.State -cin @('Running', 'Stopped')) '服务正处于过渡状态。'
    $metadata = Get-QPReleaseMetadata $Version
    Assert-QP ((ConvertTo-QPVersion $metadata.Manifest['version']) -ge [version]'0.7.0') '新管理器迁移要求已发布且已校验的 v0.7.0 或以上安装器。'
    Assert-QP ((Get-QPFileHash $Source 524288) -ceq $metadata.Sums['install.ps1']) '当前迁移安装器与所选官方发行版摘要不符。'
    $installer = Read-QPVerifiedAsset $metadata 'install.ps1'; $updater = Read-QPVerifiedAsset $metadata 'update.ps1'
    $new = $old.Clone(); $new['format'] = 3; $new['privilegeMode'] = 'restricted'; $new['privilegeConsent'] = $null
    if ($old['format'] -eq 1) { $new['accessMode'] = 'local'; if ($old['origin']) { $new['accessMode'] = 'proxy' }; $new['tls'] = $null }
    $new['installerSHA256'] = $metadata.Sums['install.ps1']; $new['updaterSHA256'] = $metadata.Sums['update.ps1']
    [void](Read-QPConfigValue $new)
    $transaction = @{ format = 3; kind = 'migrate'; phase = 'prepared'; old = $old; new = $new; wasRunning = ($service.State -ceq 'Running'); backup = [guid]::NewGuid().ToString('N') }
    Write-QPAtomicJson $Paths.Journal $transaction
    $backedUp = $false
    try {
        Stop-QPServiceAndWait $Paths $old
        $snapshot = New-QPBackupSnapshot $Paths $old $transaction['backup']; $backedUp = $true
        if ($old['format'] -eq 2) {
            Assert-QPShortcuts $Paths
            foreach ($name in @('qqon.ps1', 'qqon.cmd')) { Remove-QPManagedFile ([IO.Path]::Combine($Paths.Code, $name)) 'Code' }
        }
        Write-QPAtomicCode $Paths.Installer $installer $transaction['backup']; Write-QPAtomicCode $Paths.Updater $updater $transaction['backup']
        Write-QPShortcuts $Paths
        Write-QPAtomicJson $Paths.Config $new
        $transaction['phase'] = 'ready'; Write-QPAtomicJson $Paths.Journal $transaction
        Restore-QPOriginalService $Paths $new $transaction['wasRunning']
        Remove-QPManagedFile $Paths.Journal 'Admin'
    } catch {
        try {
            if ($backedUp) { Undo-QPMigration $Paths $transaction }
            else { Restore-QPOriginalService $Paths $old $transaction['wasRunning']; Remove-QPManagedFile $Paths.Journal 'Admin' }
        } catch { throw '旧版管理器迁移失败且恢复未完成；独立事务备份和日志已保留，原账户数据未重置。' }
        throw '旧版管理器迁移失败；旧配置、旧管理器及原服务状态已恢复，账户未重置。'
    }
    [Console]::WriteLine('旧版管理器已独立迁移为 format3 restricted；原应用、服务身份、访问方式和账户保持不变。事务备份：' + $snapshot.Path)
    [Console]::WriteLine('下一步先运行 qqon update --version v0.7.0 升级应用，再单独运行 qqon privilege 授权。管理器迁移不授权最高权限，不修改 PATH。')
    Show-QPAccessSummary $Paths $new
}

function Switch-QPBinary {
    param([hashtable]$Paths)
    Assert-QPAcl $Paths.Candidate 'Code' -File; Assert-QPAcl $Paths.Binary 'Code' -File
    Assert-QP (-not [IO.File]::Exists($Paths.Previous) -and -not [IO.Directory]::Exists($Paths.Previous)) 'Previous backup path must be cleared before the journal is created.'
    [IO.File]::Replace($Paths.Candidate, $Paths.Binary, $Paths.Previous)
}

function Restore-QPBinary {
    param([hashtable]$Paths, [hashtable]$Old)
    if ((Get-QPFileHash $Paths.Binary) -ceq $Old['binarySHA256']) { Assert-QPBinary $Paths.Binary $Old; return }
    Assert-QPBinary $Paths.Previous $Old
    Assert-QPAcl $Paths.Binary 'Code' -File
    [IO.File]::Replace($Paths.Previous, $Paths.Binary, [NullString]::Value)
    Assert-QPBinary $Paths.Binary $Old
}

function Undo-QPUpdate {
    param([hashtable]$Paths, [hashtable]$Transaction)
    $old = $Transaction['old']
    Stop-QPServiceAndWait $Paths $old
    Restore-QPBinary $Paths $old
    Write-QPAtomicJson $Paths.Config $old
    Start-QPService $Paths $old
    Wait-QPHealthy $Paths $old
    Remove-QPManagedFile $Paths.Candidate 'Code'
    Remove-QPManagedFile $Paths.Journal 'Admin'
}

function Invoke-QPUpdateTransaction {
    param([hashtable]$Paths, [hashtable]$Old, [hashtable]$New)
    Assert-QP (Test-QPSameFields $Old $New @('format', 'repository', 'service', 'listen', 'origin', 'healthHost', 'dataSchema', 'installerSHA256', 'updaterSHA256', 'accessMode', 'tls', 'privilegeMode', 'privilegeConsent')) '普通升级不能修改权限、授权或固定配置。[QP_PRIVILEGE]'
    $transaction = @{ format = $New['format']; kind = 'update'; phase = 'prepared'; old = $Old; new = $New }
    Write-QPAtomicJson $Paths.Journal $transaction
    try {
        Stop-QPServiceAndWait $Paths $Old
        Switch-QPBinary $Paths
        $transaction['phase'] = 'ready'; Write-QPAtomicJson $Paths.Journal $transaction
        Start-QPService $Paths $New
        Wait-QPHealthy $Paths $New
        Write-QPAtomicJson $Paths.Config $New
        Remove-QPManagedFile $Paths.Journal 'Admin'
    } catch {
        try { Undo-QPUpdate $Paths $transaction }
        catch { throw '升级失败且回滚未完成，事务日志已保留；账户数据未重置。请检查服务、磁盘和权限后重新确认恢复。' }
        throw '升级失败；旧版本已原子恢复并通过健康检查，账户数据未重置。'
    }
}

function Resume-QPTransaction {
    param([hashtable]$Paths)
    $transaction = Read-QPTransaction $Paths
    if ($transaction['kind'] -ceq 'migrate') { Remove-QPMigrationStaging $Paths $transaction }
    Assert-QPLayout $Paths
    if ($transaction['kind'] -ceq 'migrate') {
        $backup = Get-QPBackupDirectory $Paths $transaction['backup']
        $marker = [IO.Path]::Combine($backup, 'snapshot.json')
        if (-not [IO.File]::Exists($marker)) {
            $old = $transaction['old']
            Assert-QPKnownLegacy $old; Assert-QPManagers $Paths $old
            $current = Read-QPConfig $Paths -AllowLegacy
            Assert-QP (Test-QPSameFields $old $current @($old.Keys)) '未完成备份的迁移不得改变旧配置。'
            Restore-QPOriginalService $Paths $old $transaction['wasRunning']
            Remove-QPManagedFile $Paths.Journal 'Admin'
        } else { Undo-QPMigration $Paths $transaction }
        [Console]::WriteLine('已恢复旧版管理器和原服务状态；独立备份或部分备份保留，账户未重置，请重新确认迁移。')
        return
    }
    $identity = $transaction['new']; if ($transaction['kind'] -ceq 'backup') { $identity = $transaction['old'] }
    Assert-QPManagers $Paths $identity
    if ($transaction['kind'] -cin @('config', 'renew')) {
        Undo-QPConfigTransaction $Paths $transaction
        [Console]::WriteLine('已恢复中断前的访问配置、SCM 命令及原服务状态；账户和证书代次均保留。')
        return
    }
    if ($transaction['kind'] -ceq 'backup') {
        Restore-QPOriginalService $Paths $identity $transaction['wasRunning']
        Remove-QPManagedFile $Paths.Journal 'Admin'
        [Console]::WriteLine('中断备份的原服务状态已恢复；部分敏感备份保留在：' + (Get-QPBackupDirectory $Paths $transaction['backup']))
        return
    }
    if ($transaction['kind'] -ceq 'update') {
        Assert-QPService $Paths $transaction['old'] (Get-QPService)
        Undo-QPUpdate $Paths $transaction
        [Console]::WriteLine('已回滚中断的升级，旧版本通过健康检查；数据保持不变。如需升级请再次运行 qqon update。')
        return
    }
    $config = $transaction['new']
    if (-not [IO.File]::Exists($Paths.Binary)) { Assert-QPBinary $Paths.Candidate $config; [IO.File]::Move($Paths.Candidate, $Paths.Binary) }
    Assert-QPBinary $Paths.Binary $config
    Assert-QPActiveTls $Paths $config -Inspect
    if ($config['format'] -ge 2) { Write-QPShortcuts $Paths }
    if ((Get-QPPrivilegeMode $config) -ceq 'system') {
        foreach ($entry in (Get-QPPrivilegeEntries $Paths)) { Set-QPEntrySecurity (Get-QPPrivilegeEntryPath $Paths $entry) $entry['directory'] }
        Write-QPManagementConsent $Paths $config['privilegeConsent']
    }
    if ($null -eq (Get-QPService)) { Register-QPService $Paths $config }
    Assert-QPService $Paths $config (Get-QPService)
    Start-QPService $Paths $config
    Wait-QPHealthy $Paths $config
    Write-QPAtomicJson $Paths.Config $config
    Remove-QPManagedFile $Paths.Journal 'Admin'
    [Console]::WriteLine('中断安装已恢复并通过健康检查，已有账户数据未重置。')
    Show-QPAccessSummary $Paths $config
}

function Get-QPWindowsRecord {
    param([hashtable]$Metadata)
    $records = @($Metadata.Manifest['binaries'] | Where-Object { $_['os'] -ceq 'windows' -and $_['arch'] -ceq 'amd64' })
    Assert-QP ($records.Count -eq 1) 'Exactly one Windows amd64 binary is required.'
    return ,$records[0]
}

function New-QPInstallConfig {
    param([hashtable]$Metadata, [int]$Port, [string]$Origin, $Consent = $null)
    $mode = 'local'; if ($Origin) { $mode = 'proxy' }
    $info = Get-QPAccess $mode $Port '' $Origin; $binary = Get-QPWindowsRecord $Metadata
    $value = @{
        format = 3; privilegeMode = 'restricted'; privilegeConsent = $null
        repository = 'LinyuFiee/QQON-Panel'; service = 'QQONPanel'; listen = $info.listen
        origin = $info.origin; healthHost = $info.healthHost; accessMode = $mode; tls = $null
        version = $Metadata.Manifest['version']; dataSchema = 1
        commit = $Metadata.Manifest['commit']; buildTime = $Metadata.Manifest['buildTime']; binarySize = $binary['size']
        binarySHA256 = $binary['sha256']; installerSHA256 = $Metadata.Sums['install.ps1']; updaterSHA256 = $Metadata.Sums['update.ps1']
    }
    if ($null -ne $Consent) { $value['privilegeMode'] = 'system'; $value['privilegeConsent'] = Read-QPPrivilegeConsent $Consent }
    return Read-QPConfigValue $value
}

function Set-QPAccessValue {
    param([hashtable]$Config, [hashtable]$Access, $Tls)
    $new = $Config.Clone()
    foreach ($key in @('listen', 'origin', 'healthHost', 'accessMode')) { $new[$key] = $Access[$key] }
    $new['tls'] = $Tls
    return Read-QPConfigValue $new
}

function Install-QPPanel {
    param([hashtable]$Paths, [int]$Port, [string]$Origin, [string]$Version, [string]$Source, [hashtable]$AccessOptions = $null, $Consent = $null)
    foreach ($path in @($Paths.Code, $Paths.State)) {
        Assert-QPPlainPath $path -AllowMissing
        Assert-QP (-not [IO.Directory]::Exists($path) -and -not [IO.File]::Exists($path)) '安装目录已存在。已有安装请使用状态、恢复或独立迁移，拒绝接管未知目录。'
        Assert-QPInstallParent ([IO.Path]::GetDirectoryName($path))
    }
    Assert-QP ($null -eq (Get-QPService)) 'QQONPanel 服务已存在，拒绝接管。'
    $metadata = Get-QPReleaseMetadata $Version
    Assert-QP ((ConvertTo-QPVersion $metadata.Manifest['version']) -ge [version]'0.6.0') '此安装器新装要求已发布的 v0.6.0 或以上；v0.5 请使用原已验证旧安装入口。'
    $installer = Read-QPVerifiedAsset $metadata 'install.ps1'
    Assert-QP ((Get-QPFileHash $Source 524288) -ceq $metadata.Sums['install.ps1']) '请运行所选发行版的已校验安装器，当前文件摘要不匹配。'
    $updater = Read-QPVerifiedAsset $metadata 'update.ps1'
    $archive = Read-QPVerifiedAsset $metadata 'panel-windows-amd64.zip'
    $config = New-QPInstallConfig $metadata $Port $Origin $Consent
    if ($null -ne $Consent) { Assert-QP ((ConvertTo-QPVersion $config['version']) -ge [version]'0.7.0') '最高权限授权需要应用 v0.7.0 或以上，不能授权旧程序。[QP_PRIVILEGE]' }
    $access = Get-QPAccess $config['accessMode'] $Port '' $Origin
    if ($null -ne $AccessOptions) { $access = Get-QPAccess $AccessOptions.Mode $Port $AccessOptions.Host $Origin }
    $journaled = $false
    try {
        New-QPPrivateDirectory $Paths.Code (Get-QPStorageKind $config 'Code')
        New-QPPrivateDirectory $Paths.State (Get-QPStorageKind $config 'State')
        New-QPPrivateDirectory $Paths.Data (Get-QPStorageKind $config 'Data')
        Write-QPNewFile $Paths.Installer $installer 'Code'
        Write-QPNewFile $Paths.Updater $updater 'Code'
        Write-QPShortcuts $Paths
        Expand-QPWindowsZip $archive (Get-QPWindowsRecord $metadata) $Paths.Candidate
        Assert-QPBinary $Paths.Candidate $config
        $tls = $null
        if ($access.accessMode -cin @('public-selfsigned', 'public-certificate')) {
            $tls = New-QPTlsGeneration $Paths $config $access $Paths.Candidate $AccessOptions.Certificate $AccessOptions.PrivateKey
        }
        $config = Set-QPAccessValue $config $access $tls
        $transaction = @{ format = $config['format']; kind = 'install'; phase = 'prepared'; old = $null; new = $config }
        Write-QPAtomicJson $Paths.Journal $transaction; $journaled = $true
        [IO.File]::Move($Paths.Candidate, $Paths.Binary)
        if ((Get-QPPrivilegeMode $config) -ceq 'system') {
            foreach ($entry in (Get-QPPrivilegeEntries $Paths)) { Set-QPEntrySecurity (Get-QPPrivilegeEntryPath $Paths $entry) $entry['directory'] }
            Write-QPManagementConsent $Paths $config['privilegeConsent']
        }
        Register-QPService $Paths $config
        $transaction['phase'] = 'ready'; Write-QPAtomicJson $Paths.Journal $transaction
        Start-QPService $Paths $config
        Wait-QPHealthy $Paths $config
        Write-QPAtomicJson $Paths.Config $config
        Remove-QPManagedFile $Paths.Journal 'Admin'
    } catch {
        if ($journaled) {
            $stopped = $true
            try { if ($null -ne (Get-QPService)) { Stop-QPServiceAndWait $Paths $config } } catch { $stopped = $false }
            if (-not $stopped) { throw '安装未完成且服务停止未确认；事务、证书和账户目录均保留，请先修复后确认恢复。' }
            throw '安装未完成，服务已停止；事务、证书和数据保留，请修复后使用安装器更新入口确认恢复。'
        }
        throw '安装在服务注册前失败；可能存在的私有目录已保留供管理员检查，未删除账户数据或旧证书。'
    }
    [Console]::WriteLine('已安装 QQON-Panel ' + $config['version'] + '；固定 SCM 服务及精确版本健康检查通过。')
    Show-QPAccessSummary $Paths $config
}

function Update-QPPanel {
    param([hashtable]$Paths, [string]$Version)
    if ([IO.File]::Exists((Get-QPPrivilegeJournalPath $Paths))) { Resume-QPPrivilege $Paths; return }
    if ([IO.File]::Exists($Paths.Journal)) { Resume-QPTransaction $Paths; return }
    Assert-QPLayout $Paths
    $old = Read-QPConfig $Paths
    Assert-QPManagers $Paths $old; Assert-QPBinary $Paths.Binary $old
    Assert-QPService $Paths $old (Get-QPService)
    $metadata = Get-QPReleaseMetadata $Version
    $nextVersion = ConvertTo-QPVersion $metadata.Manifest['version']; $oldVersion = ConvertTo-QPVersion $old['version']
    Assert-QP ($nextVersion -ge $oldVersion) 'Downgrades are forbidden; no files were switched.'
    if ($nextVersion -eq $oldVersion) {
        $record = Get-QPWindowsRecord $metadata
        Assert-QP ($record['sha256'] -ceq $old['binarySHA256'] -and $record['size'] -eq $old['binarySize']) 'Same-version release was changed; refusing it.'
        foreach ($key in @('commit', 'buildTime', 'repository', 'dataSchema')) { Assert-QP ($metadata.Manifest[$key] -ceq $old[$key]) 'Same-version release identity changed.' }
        [Console]::WriteLine('当前已是 ' + $old['version'] + '，未执行更新。'); return
    }
    $new = $old.Clone(); $binary = Get-QPWindowsRecord $metadata
    foreach ($key in @('version', 'commit', 'buildTime')) { $new[$key] = $metadata.Manifest[$key] }
    $new['binarySize'] = $binary['size']; $new['binarySHA256'] = $binary['sha256']
    [void](Read-QPConfigValue $new)
    Assert-QP ($new['binarySHA256'] -cne $old['binarySHA256']) 'A newer version cannot reuse the old executable identity.'
    $archive = Read-QPVerifiedAsset $metadata 'panel-windows-amd64.zip'
    # The installed reviewed manager is deliberately stable across ordinary binary updates.
    # No unjournaled self-update or unknown manager migration is attempted.
    Remove-QPManagedFile $Paths.Candidate 'Code'
    Remove-QPManagedFile $Paths.Previous 'Code'
    try {
        Expand-QPWindowsZip $archive $binary $Paths.Candidate
        if ((Get-QPPrivilegeMode $new) -ceq 'system') { Set-QPEntrySecurity $Paths.Candidate $false }
        Assert-QPBinary $Paths.Candidate $new
        Invoke-QPUpdateTransaction $Paths $old $new
    } finally {
        if (-not [IO.File]::Exists($Paths.Journal)) { Remove-QPManagedFile $Paths.Candidate 'Code' }
    }
    [Console]::WriteLine('已升级到 ' + $new['version'] + '；固定服务通过精确版本及数据格式健康检查，账户数据保留。')
    Show-QPAccessSummary $Paths $new
}

function Get-QPWebUrl {
    param([hashtable]$Config)
    if ($Config['origin']) { return $Config['origin'] + '/' }
    return 'http://' + $Config['listen'] + '/'
}

function Show-QPAccessSummary {
    param([hashtable]$Paths, [hashtable]$Config)
    [Console]::WriteLine('实际配置的 WebUI 地址：' + (Get-QPWebUrl $Config))
    [Console]::WriteLine('监听地址：' + $Config['listen'] + '；TCP 端口：' + $Config['listen'].Split([char]':')[1])
    if ($null -ne $Config['tls']) {
        [Console]::WriteLine('证书 SHA-256 指纹：' + $Config['tls']['fingerprint'])
        [Console]::WriteLine('证书有效至（UTC）：' + $Config['tls']['notAfter'])
        [Console]::WriteLine('证书 SAN 主机：' + $Config['tls']['host'])
        if ($Config['accessMode'] -ceq 'public-selfsigned') { [Console]::WriteLine('自签证书会触发浏览器警告；请通过可信渠道核对指纹。安装器未自动信任根证书。') }
        else { [Console]::WriteLine('导入证书链由管理员提供；安装器不保证公网浏览器信任该链。') }
    }
    if ($Config['origin'] -and $null -eq $Config['tls']) { [Console]::WriteLine('这是已有 HTTPS 反向代理的配置地址；本机未创建或验证外部代理。') }
    [Console]::WriteLine('外网可达性：未检测。未修改本机防火墙；请自行检查上述 TCP 端口、云安全组和 NAT 转发。本轮仅 IPv4，不代表 IPv6 双栈。')
    [Console]::WriteLine('账户数据路径：' + $Paths.Data)
    [Console]::WriteLine('首次初始化令牌仅在需要时从此私有路径读取：' + [IO.Path]::Combine($Paths.Data, 'setup-token'))
    [Console]::WriteLine('维护快捷入口：' + [IO.Path]::Combine($Paths.Code, 'qqon.cmd'))
}

function Show-QPStatus {
    param([hashtable]$Paths)
    # No locks, recovery, downloads, certificate creation or binary execution.
    $codeExists = [IO.Directory]::Exists($Paths.Code); $stateExists = [IO.Directory]::Exists($Paths.State)
    $service = Get-QPService
    if (-not $codeExists -and -not $stateExists -and $null -eq $service) { [Console]::WriteLine('尚未安装 QQON-Panel。'); return }
    Assert-QPLayout $Paths
    if ([IO.File]::Exists($Paths.Journal)) {
        $journal = Read-QPTransaction $Paths
        [Console]::WriteLine('发现未完成事务：' + $journal['kind'] + '（' + $journal['phase'] + '）。只读状态没有恢复或修改任何文件，请用已校验安装器确认恢复。')
        if ($null -ne $service) { [Console]::WriteLine('实际 SCM 状态：' + (Get-QPServiceStateText $service.State)) }
        return
    }
    $config = Read-QPConfig $Paths -AllowLegacy
    if ($config['format'] -eq 1) { Assert-QPKnownLegacy $config }
    Assert-QPManagers $Paths $config; Assert-QPBinary $Paths.Binary $config -ReadOnly
    Assert-QPService $Paths $config $service
    Assert-QPActiveTls $Paths $config
    [Console]::WriteLine('QQON-Panel ' + $config['version'] + '；实际 SCM 状态：' + (Get-QPServiceStateText $service.State) + '；服务身份：' + $service.StartName)
    [Console]::WriteLine('提交：' + $config['commit'] + '；数据格式：1；配置格式：' + $config['format'])
    Show-QPAccessSummary $Paths $config
    if ($null -ne $config['tls']) {
        $days = ((Read-QPUtcTime $config['tls']['notAfter']) - [datetimeoffset]::UtcNow).TotalDays
        if ($days -le 30) { [Console]::WriteLine('证书即将到期或已过期，请运行 qqon tls renew；导入证书请使用 qqon config 重新提供证书。') }
    }
    if ($config['format'] -eq 1) { [Console]::WriteLine('检测到官方旧版管理器，需通过已验证新安装器单独确认迁移；状态查询不会自动迁移。') }
}

function Get-QPServiceStateText {
    param([string]$State)
    switch -CaseSensitive ($State) {
        'Running' { return '运行中' }; 'Stopped' { return '已停止' }; 'Start Pending' { return '启动中' }
        'Stop Pending' { return '停止中' }; 'Paused' { return '已暂停' }; default { return '过渡或未知状态' }
    }
}

function Show-QPUrl {
    param([hashtable]$Paths)
    Assert-QPAcl $Paths.Code 'Code' -Root; Assert-QPAcl $Paths.State 'State' -Root
    $config = Read-QPConfig $Paths -AllowLegacy
    if ($config['format'] -eq 1) { Assert-QPKnownLegacy $config }
    Assert-QPManagers $Paths $config
    $service = Get-QPService; Assert-QPService $Paths $config $service
    [Console]::WriteLine('WebUI：' + (Get-QPWebUrl $config))
    [Console]::WriteLine('本机服务：' + (Get-QPServiceStateText $service.State) + '；外网可达性：未检测。')
}

function Read-QPRuntimeLog {
    param([hashtable]$Paths, [int]$Lines = 50)
    Assert-QP ($Lines -ge 1 -and $Lines -le 200) '日志行数必须为 1–200，默认 50。'
    $directory = [IO.Path]::Combine($Paths.Data, 'logs'); $path = [IO.Path]::Combine($directory, 'runtime.log')
    Assert-QPPlainPath $path -AllowMissing
    if (-not [IO.File]::Exists($path)) { [Console]::WriteLine('运行日志尚不存在：' + $path); return }
    Assert-QPAcl $Paths.Data 'Data' -Root; Assert-QPAcl $directory 'Data'; Assert-QPAcl $path 'Data' -File
    $stream = [IO.FileStream]::new($path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]'ReadWrite, Delete')
    try {
        $length = [Math]::Min([long]131072, $stream.Length); $offset = $stream.Length - $length
        [void]$stream.Seek($offset, [IO.SeekOrigin]::Begin)
        $bytes = New-Object byte[] ([int]$length); $read = 0
        while ($read -lt $bytes.Length) { $n = $stream.Read($bytes, $read, $bytes.Length - $read); if ($n -eq 0) { break }; $read += $n }
        $text = [Text.UTF8Encoding]::new($false).GetString($bytes, 0, $read)
        $entries = @($text -split "`n")
        if ($offset -gt 0 -and $entries.Count -gt 0) { $entries[0] = '' }
        $start = [Math]::Max(0, $entries.Count - $Lines - 1)
        $result = [Collections.Generic.List[string]]::new(); $inPem = $false
        foreach ($line in $entries) {
            $clean = [regex]::Replace($line, '[\x00-\x08\x0b-\x1f\x7f]', '')
            if ($clean -match '-----BEGIN .*PRIVATE KEY-----') { $inPem = $true; $clean = '[已隐藏敏感密钥行]' }
            elseif ($inPem) { if ($clean -match '-----END .*PRIVATE KEY-----') { $inPem = $false }; $clean = '[已隐藏敏感密钥行]' }
            elseif ($clean -match '(?i)token|password|passwd|secret|authorization|cookie|private.?key|私钥|密码|口令|令牌') { $clean = '[已隐藏可能包含凭据的日志行]' }
            [void]$result.Add($clean)
        }
        $selected = @($result | Select-Object -Skip $start | Where-Object { $_.Length -gt 0 } | Select-Object -Last $Lines)
        [Console]::WriteLine('运行日志（只读，最多 128 KiB）：' + $path)
        foreach ($line in $selected) { [Console]::WriteLine($line) }
    } finally { $stream.Dispose() }
}

function Read-QPConsole {
    param([string]$Prompt)
    Assert-QP (-not [Console]::IsInputRedirected) '交互操作需要真实终端；自动操作请明确指定 -Action 和 -Yes 及全部所需参数。'
    [Console]::Write($Prompt)
    $value = [Console]::ReadLine()
    Assert-QP ($null -ne $value) '终端输入已经结束。'
    return $value
}

function Confirm-QPChange {
    param([string]$Description, [bool]$Yes)
    if ($Yes) { return }
    Assert-QP ((Read-QPConsole ($Description + ' 输入“确认”继续，其他输入取消：')) -ceq '确认') '未确认操作，未开始修改。'
}

function Read-QPAccessOptions {
    param([hashtable]$Options)
    [Console]::WriteLine('访问方式：1 公网 IPv4 自签 HTTPS（默认）  2 导入 HTTPS 证书  3 仅本机/SSH 隧道  4 已有 HTTPS 反向代理')
    $mode = (Read-QPConsole '请选择 [1]：').Trim(); if (-not $mode) { $mode = '1' }
    Assert-QP ($mode -cin @('1', '2', '3', '4')) '请选择 1、2、3 或 4。'
    $map = @{ '1' = 'public-selfsigned'; '2' = 'public-certificate'; '3' = 'local'; '4' = 'proxy' }
    $Options['AccessMode'] = $map[$mode]; $Options['Origin'] = ''; $Options['PublicHost'] = ''; $Options['Certificate'] = ''; $Options['PrivateKey'] = ''
    $port = Read-QPConsole '监听 TCP 端口 [8443，范围 1024–65535]：'
    $Options['Port'] = 8443
    if ($port) { Assert-QP ($port -cmatch '\A[1-9][0-9]{3,4}\z') '请输入规范端口数字。'; $Options['Port'] = [int]$port }
    if ($mode -cin @('1', '2')) {
        $Options['PublicHost'] = Read-QPConsole '请确认服务器公网 IPv4（导入模式也允许证书域名）；不会请求外部发现服务：'
        if ($mode -ceq '2') {
            $Options['Certificate'] = Read-QPConsole 'PEM 证书链的绝对本地文件路径（来源路径不保存）：'
            $Options['PrivateKey'] = Read-QPConsole '未加密 PEM 私钥的绝对本地文件路径（内容不显示、来源路径不保存）：'
        }
    } elseif ($mode -ceq '4') { $Options['Origin'] = Read-QPConsole '请输入已配置代理的 HTTPS 域名地址（本机不安装代理）：' }
    [void](Get-QPAccess $Options['AccessMode'] $Options['Port'] $Options['PublicHost'] $Options['Origin'])
}

function Invoke-QPConfigure {
    param([hashtable]$Paths, [hashtable]$Config, [switch]$Renew, [bool]$Yes)
    Assert-QP ($Config['format'] -ge 2) '访问配置仅支持 format2/format3，请先单独确认旧版管理器迁移。'
    if ($Renew) {
        Assert-QP ($Config['accessMode'] -ceq 'public-selfsigned') '续期仅支持自签模式；导入证书请运行 qqon config 并重新提供证书和密钥。'
        $options = @{ AccessMode = 'public-selfsigned'; Port = [int]$Config['listen'].Split([char]':')[1]; PublicHost = $Config['tls']['host']; Origin = ''; Certificate = ''; PrivateKey = '' }
    } else {
        $options = @{}; Read-QPAccessOptions $options
        Confirm-QPChange ('切换至 ' + $options['AccessMode'] + '，端口 ' + $options['Port'] + '；保留账户，短暂停止服务并健康检查') $Yes
    }
    $access = Get-QPAccess $options['AccessMode'] $options['Port'] $options['PublicHost'] $options['Origin']
    $tls = $null
    if ($access.accessMode -cin @('public-selfsigned', 'public-certificate')) { $tls = New-QPTlsGeneration $Paths $Config $access $Paths.Binary $options['Certificate'] $options['PrivateKey'] }
    $new = Set-QPAccessValue $Config $access $tls
    $kind = 'config'; if ($Renew) { $kind = 'renew' }
    Invoke-QPConfigTransaction $Paths $Config $new $kind
    [Console]::WriteLine('访问配置已提交；原服务状态已恢复，账户数据未重置。')
    Show-QPAccessSummary $Paths $new
}

function Show-QPMaintenanceHelp {
    [Console]::WriteLine(@'
QQON-Panel 维护命令（只管理固定服务 QQONPanel）
  qqon                         中文维护菜单
  qqon status                  查看实际服务状态、版本和配置（只读）
  qqon url                     显示 WebUI 地址，外网可达性未检测（只读）
  qqon logs [--lines 1-200]     查看固定运行日志，默认 50 行（只读）
  qqon start|stop|restart       启动、停止或重启服务
  qqon update [--version vX.Y.Z] 更新应用；不重置账户
  qqon backup                  停服一致性私有备份，随后恢复原服务状态
  qqon config                  修改访问配置或导入新证书，失败回滚
  qqon privilege               独立授权 LocalSystem，需专门输入“同意最高权限”
  qqon privilege --accept-system-management [--yes]  独立非交互授权
  qqon tls renew               自签证书续期，使用新的不可变代次
修改操作需要管理员及确认；--yes 不授权最高权限，privilege 必须另用专用参数或中文同意。
所有快捷入口均由已校验安装器生成，不增加发行资产。不修改防火墙或系统证书信任。
'@)
}

function Read-QPMaintenanceCommand {
    param([string[]]$Words)
    # The same exact whitelist also appears in the generated wrapper; test parity.
    if ($null -eq $Words -or $Words.Count -eq 0) { $Words = @('menu') }
    Assert-QP ($Words.Count -le 5) '维护命令参数过多。'
    foreach ($word in $Words) { Assert-QP ($word -cmatch '\A[a-zA-Z0-9.-]{1,64}\z') '维护参数不在 ASCII 白名单中。' }
    $action = $Words[0]; $index = 1; $yes = $false; $version = ''; $lines = 50; $accept = $false
    Assert-QP ($action -cin @('menu', 'help', '--help', 'status', 'url', 'start', 'stop', 'restart', 'logs', 'update', 'backup', 'config', 'tls', 'privilege')) '未知维护命令，请输入 qqon help。'
    if ($action -ceq 'tls') { Assert-QP ($Words.Count -ge 2 -and $Words[1] -ceq 'renew') '证书续期命令为 qqon tls renew。'; $index = 2 }
    while ($index -lt $Words.Count) {
        $arg = $Words[$index]; $index++
        if ($arg -ceq '--yes' -and $action -cin @('start', 'stop', 'restart', 'update', 'backup', 'config', 'tls', 'privilege') -and -not $yes) { $yes = $true; continue }
        if ($arg -ceq '--accept-system-management' -and $action -ceq 'privilege' -and -not $accept) { $accept = $true; continue }
        if ($arg -ceq '--version' -and $action -ceq 'update' -and -not $version -and $index -lt $Words.Count) { $version = $Words[$index]; $index++; [void](ConvertTo-QPVersion $version -Tagged); continue }
        if ($arg -ceq '--lines' -and $action -ceq 'logs' -and $Words.Count -eq 3 -and $index -lt $Words.Count) { $text = $Words[$index]; $index++; Assert-QP ($text -cmatch '\A[1-9][0-9]{0,2}\z' -and [int]$text -le 200) '日志行数必须为 1–200。'; $lines = [int]$text; continue }
        throw '维护参数不匹配或重复，请输入 qqon help。'
    }
    if ($action -ceq '--help') { $action = 'help' }
    return @{ Action = $action; Yes = $yes; Version = $version; Lines = $lines; AcceptSystemManagement = $accept }
}

function Invoke-QPMaintenance {
    param([string[]]$Words)
    $command = Read-QPMaintenanceCommand $Words
    if ($command.Action -ceq 'help') { Show-QPMaintenanceHelp; return }
    if ($command.Action -ceq 'menu') {
        [Console]::WriteLine('QQON 维护：1 状态  2 地址  3 日志  4 启动  5 停止  6 重启  7 更新  8 备份  9 访问配置  10 自签续期  11 最高权限独立授权  0 退出')
        $selected = Read-QPConsole '请选择：'
        if ($selected -ceq '0') { return }
        $menu = @{ '1' = @('status'); '2' = @('url'); '3' = @('logs'); '4' = @('start'); '5' = @('stop'); '6' = @('restart'); '7' = @('update'); '8' = @('backup'); '9' = @('config'); '10' = @('tls', 'renew'); '11' = @('privilege') }
        Assert-QP ($menu.ContainsKey($selected)) '菜单选择无效。'
        Invoke-QPMaintenance $menu[$selected]; return
    }
    $readOnly = $command.Action -cin @('status', 'url', 'logs')
    Assert-QPNativeHost -Administrator:(-not $readOnly)
    $paths = Get-QPPaths
    if ($readOnly) {
        switch ($command.Action) { 'status' { Show-QPStatus $paths }; 'url' { Show-QPUrl $paths }; 'logs' { Read-QPRuntimeLog $paths $command.Lines } }
        return
    }
    $descriptions = @{ start = '启动服务'; stop = '停止服务'; restart = '重启服务'; update = '更新应用或恢复中断事务'; backup = '停服备份（包含账户及 TLS 私钥）'; config = '修改访问配置'; tls = '生成全新自签证书并切换配置' }
    $consent = $null
    if ($command.Action -ceq 'privilege') {
        $consent = Confirm-QPSystemManagement $command.AcceptSystemManagement $command.Yes
        if ($null -eq $consent) { [Console]::WriteLine('未取得专门同意，未变更服务身份。'); return }
    } else { Confirm-QPChange $descriptions[$command.Action] $command.Yes }
    $mutex = Enter-QPLock
    try {
        if ([IO.File]::Exists((Get-QPPrivilegeJournalPath $paths))) { Resume-QPPrivilege $paths; return }
        if ([IO.File]::Exists($paths.Journal)) { Resume-QPTransaction $paths; return }
        Assert-QPLayout $paths
        $config = Read-QPConfig $paths
        Assert-QPManagers $paths $config; Assert-QPShortcuts $paths; Assert-QPBinary $paths.Binary $config
        Assert-QPService $paths $config (Get-QPService); Assert-QPActiveTls $paths $config
        switch ($command.Action) {
            'start' { Start-QPService $paths $config; Wait-QPHealthy $paths $config }
            'stop' { Stop-QPServiceAndWait $paths $config }
            'restart' { Stop-QPServiceAndWait $paths $config; Start-QPService $paths $config; Wait-QPHealthy $paths $config }
            'update' { Update-QPPanel $paths $command.Version }
            'backup' { Invoke-QPBackup $paths $config }
            'config' { Invoke-QPConfigure $paths $config -Yes:$command.Yes }
            'tls' { Invoke-QPConfigure $paths $config -Renew -Yes:$command.Yes }
            'privilege' { Invoke-QPPrivilege $paths $config $consent }
        }
        if ($command.Action -cin @('start', 'stop', 'restart')) { [Console]::WriteLine('服务操作完成：' + $descriptions[$command.Action]) }
    } finally { Exit-QPLock $mutex }
}

function Invoke-QPInstaller {
    param([hashtable]$Options, [string]$Source)
    if ($Options['Maintenance']) {
        Assert-QP (-not $Options['Action'] -and -not $Options['PortSpecified'] -and -not $Options['OriginSpecified']) '维护入口不能混用安装配置参数。'
        $words = @(([string]$Options['Maintenance']).Split([char]' ', [StringSplitOptions]::RemoveEmptyEntries))
        if ($null -ne $Options['MaintenanceArguments']) { $words += @($Options['MaintenanceArguments']) }
        if ($Options['AcceptSystemManagement']) { Assert-QP ($words[0] -ceq 'privilege') '-AcceptSystemManagement 只适用于独立 privilege 授权或新安装。[QP_PRIVILEGE]'; $words += '--accept-system-management' }
        if ($Options['Yes']) { $words += '--yes' }
        Invoke-QPMaintenance $words; return
    }
    $action = [string]$Options['Action']
    if (-not $action) {
        [Console]::WriteLine('QQON-Panel：1 安装  2 更新/恢复  3 状态（只读）  4 迁移官方 v0.5/v0.6 管理器  0 退出')
        switch ((Read-QPConsole '请选择：').Trim()) {
            '1' { $action = 'Install'; Read-QPAccessOptions $Options }
            '2' { $action = 'Update' }; '3' { $action = 'Status' }
            '4' { $action = 'Migrate' }; '0' { return }; default { throw '请选择 1、2、3、4 或 0。' }
        }
    }
    Assert-QP ($action -cin @('Install', 'Update', 'Status', 'Migrate')) '安装动作只支持 Install、Update、Status、Migrate。'
    Assert-QP (-not $Options['AcceptSystemManagement'] -or $action -ceq 'Install') '更新或管理器迁移不能携带最高权限同意；请使用独立 -Maintenance privilege。[QP_PRIVILEGE]'
    if ($action -ceq 'Status') {
        Assert-QP (-not $Options['PortSpecified'] -and -not $Options['OriginSpecified']) '只读状态不接受修改配置参数。'
        Assert-QPNativeHost; Show-QPStatus (Get-QPPaths); return
    }
    Assert-QPNativeHost -Administrator
    if ($Options['Version']) { [void](ConvertTo-QPVersion $Options['Version'] -Tagged) }
    if ($action -ceq 'Install') {
        if (-not $Options.ContainsKey('AccessMode')) { $Options['AccessMode'] = 'public-selfsigned' }
        if (-not $Options.ContainsKey('Port')) { $Options['Port'] = 8443 }
        $access = Get-QPAccess $Options['AccessMode'] $Options['Port'] $Options['PublicHost'] $Options['Origin']
        [Console]::WriteLine('将配置监听 ' + $access.listen + '；WebUI：' + $(if ($access.origin) { $access.origin + '/' } else { 'http://' + $access.listen + '/' }))
    } else { Assert-QP (-not $Options['PortSpecified'] -and -not $Options['OriginSpecified']) '更新或迁移不得改变原有访问设置。' }
    [Console]::WriteLine('仅管理固定 QQONPanel 服务、私有程序和数据目录；不重置账户，不改防火墙、全局执行策略或系统根证书。')
    if ($action -ceq 'Migrate') {
        Assert-QP (-not $Options['Yes'] -or $Options['MigrateLegacy']) '自动迁移必须同时明确指定 -Yes -MigrateLegacy，不与普通更新混用。'
        Confirm-QPChange '单独迁移已知官方 v0.5/v0.6 管理器：先独立私有备份，保持旧应用及原访问方式' ([bool]$Options['MigrateLegacy'])
    } else { Confirm-QPChange $(if ($action -ceq 'Install') { '安装并启用以上配置' } else { '更新应用或恢复中断事务' }) ([bool]$Options['Yes']) }
    $pathConsent = $false; $systemConsent = $null
    if ($action -ceq 'Install') {
        $systemConsent = Confirm-QPSystemManagement ([bool]$Options['AcceptSystemManagement']) ([bool]$Options['Yes'])
        [Console]::WriteLine('可选：仅把固定 ProgramFiles\QQON-Panel 追加到系统 PATH；不改已有条目，当前父终端不更新。不加入时使用绝对路径 qqon.cmd。')
        $pathConsent = [bool]$Options['AddToPath']
        if (-not $Options['Yes'] -and -not $pathConsent) { $pathConsent = ((Read-QPConsole '是否追加系统 PATH？输入“确认”同意，回车跳过：') -ceq '确认') }
    }
    $paths = Get-QPPaths; $mutex = Enter-QPLock
    try {
        if ($action -ceq 'Install') {
            $accessOptions = @{ Mode = $Options['AccessMode']; Host = [string]$Options['PublicHost']; Certificate = [string]$Options['Certificate']; PrivateKey = [string]$Options['PrivateKey'] }
            Install-QPPanel $paths ([int]$Options['Port']) ([string]$Options['Origin']) ([string]$Options['Version']) $Source $accessOptions $systemConsent
            if ($pathConsent) { Add-QPSystemPath $paths } else { [Console]::WriteLine('未修改系统 PATH。请使用：' + [IO.Path]::Combine($paths.Code, 'qqon.cmd')) }
        } elseif ($action -ceq 'Migrate') { Invoke-QPMigration $paths ([string]$Options['Version']) $Source }
        else { Update-QPPanel $paths ([string]$Options['Version']) }
    } finally { Exit-QPLock $mutex }
}

# Dot-source loads definitions only. Tests mock functions, never production path/env flags.
if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference = 'Stop'
    $options = @{ Action = $Action; Port = $Port; Origin = $Origin; Version = $Version; Yes = [bool]$Yes; AcceptSystemManagement = [bool]$AcceptSystemManagement; AccessMode = $AccessMode; PublicHost = $PublicHost; Certificate = $Certificate; PrivateKey = $PrivateKey; AddToPath = [bool]$AddToPath; MigrateLegacy = [bool]$MigrateLegacy; Maintenance = $Maintenance; MaintenanceArguments = $MaintenanceArguments; PortSpecified = $PSBoundParameters.ContainsKey('Port'); OriginSpecified = $PSBoundParameters.ContainsKey('Origin') }
    try { Invoke-QPInstaller $options $PSCommandPath }
    catch { [Console]::Error.WriteLine('QQON-Panel 安装或维护未完成：' + (Get-QPFailureText $_.Exception.Message)); exit 1 }
}
