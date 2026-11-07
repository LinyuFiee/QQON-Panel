<#
.SYNOPSIS
QQON-Panel 固定更新入口；仅执行已校验的本机管理器。
.DESCRIPTION
示例：powershell -File update.ps1 -Version v0.6.0 -Yes。
不会下载并执行另一份脚本，不修改全局执行策略、PATH 或系统证书信任。
#>
[CmdletBinding()]
param([string]$Version = '', [switch]$Yes)

# BEGIN TRUSTED WRAPPER
function Get-QPWrapperPaths {
    $code = [IO.Path]::Combine([Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFiles), 'QQON-Panel')
    $state = [IO.Path]::Combine([Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData), 'QQON-Panel')
    return @{ Code = $code; State = $state; Installer = [IO.Path]::Combine($code, 'install.ps1'); Updater = [IO.Path]::Combine($code, 'update.ps1'); Config = [IO.Path]::Combine($state, 'install.json') }
}

function Assert-QPWrapperPath {
    param([string]$Path, [switch]$File, [switch]$AdminOnly, [switch]$State)
    if ($Path -cnotmatch '\A[A-Za-z]:\\' -or [IO.Path]::GetFullPath($Path) -cne $Path -or $Path.Substring(2) -match ':' -or $Path -match '[\x00-\x1f<>"|?*]') { throw '已安装快捷入口路径不规范。' }
    $current = $Path.Substring(0, 3)
    foreach ($part in $Path.Substring(3).Split([char]'\')) {
        if ($part -in @('', '.', '..') -or $part -match '[ .]\z') { throw '快捷入口路径包含不明确的目录名。' }
        $current = [IO.Path]::Combine($current, $part)
        if (([IO.File]::GetAttributes($current) -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw '快捷入口路径不能包含重解析点或链接。' }
    }
    if ($File) { $acl = [IO.File]::GetAccessControl($Path) } else { $acl = [IO.Directory]::GetAccessControl($Path) }
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin @('S-1-5-18', 'S-1-5-32-544')) { throw '快捷入口文件或目录必须由管理员或 SYSTEM 拥有。' }
    if (-not $File -and -not $acl.AreAccessRulesProtected) { throw '快捷入口根目录必须使用受保护 ACL。' }
    $sha = [Security.Cryptography.SHA1]::Create()
    try { $hash = $sha.ComputeHash([Text.Encoding]::Unicode.GetBytes('QQONPANEL')) } finally { $sha.Dispose() }
    $parts = @(for ($i = 0; $i -lt 20; $i += 4) { [BitConverter]::ToUInt32($hash, $i).ToString([Globalization.CultureInfo]::InvariantCulture) })
    $serviceSid = 'S-1-5-80-' + ($parts -join '-')
    $seen = @{}
    foreach ($rule in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
        $sid = $rule.IdentityReference.Value; $rights = [long]$rule.FileSystemRights
        if ($rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or $rule.PropagationFlags -ne [Security.AccessControl.PropagationFlags]::None) { throw '快捷入口 ACL 包含不支持的授权规则。' }
        if ($sid -in @('S-1-5-18', 'S-1-5-32-544')) {
            if (($rights -band [long][Security.AccessControl.FileSystemRights]::FullControl) -ne [long][Security.AccessControl.FileSystemRights]::FullControl) { throw '管理员或 SYSTEM 缺少完整控制权限。' }
            $seen[$sid] = $true
        } else {
            $max = [long][Security.AccessControl.FileSystemRights]'ReadAndExecute, Synchronize'
            if ($State) { $max = [long][Security.AccessControl.FileSystemRights]'Traverse, Synchronize' }
            if ($AdminOnly -or $sid -cne $serviceSid -or (($rights -band (-bnot $max)) -ne 0)) { throw '快捷入口 ACL 允许了管理员及只读服务以外的访问。' }
        }
        if (-not $File -and -not ($State -and $sid -ceq $serviceSid) -and $rule.InheritanceFlags -ne [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit') { throw '快捷入口根目录 ACL 必须保护后续创建的文件。' }
    }
    if (-not $seen.ContainsKey('S-1-5-18') -or -not $seen.ContainsKey('S-1-5-32-544')) { throw '快捷入口缺少管理员或 SYSTEM 授权。' }
}

function Get-QPWrapperHash {
    param([string]$Path, [int]$Limit)
    $stream = [IO.FileStream]::new($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        if ($stream.Length -lt 1 -or $stream.Length -gt $Limit) { throw '已安装管理器文件大小无效。' }
        return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '').ToLowerInvariant()
    } finally { $sha.Dispose(); $stream.Dispose() }
}

function Read-QPWrapperCommand {
    param([string[]]$Commands)
    if ($null -eq $Commands -or $Commands.Count -eq 0) { $Commands = @('menu') }
    if ($Commands.Count -gt 5) { throw '维护命令参数过多。输入 qqon help 查看帮助。' }
    foreach ($word in $Commands) { if ($word -cnotmatch '\A[a-zA-Z0-9.-]{1,64}\z') { throw '维护参数仅允许 ASCII 白名单，不能包含空格、路径或命令片段。' } }
    $action = $Commands[0]
    if ($action -cnotin @('menu', 'help', '--help', 'status', 'url', 'start', 'stop', 'restart', 'logs', 'update', 'backup', 'config', 'tls', 'privilege')) { throw '未知维护命令；请输入 qqon help。' }
    $yes = $false; $version = ''; $lines = 50; $index = 1; $accept = $false
    if ($action -ceq 'tls') {
        if ($Commands.Count -lt 2 -or $Commands[1] -cne 'renew') { throw '证书续期命令为 qqon tls renew。' }
        $index = 2
    }
    while ($index -lt $Commands.Count) {
        $arg = $Commands[$index]; $index++
        if ($arg -ceq '--yes' -and $action -cin @('start', 'stop', 'restart', 'update', 'backup', 'config', 'tls', 'privilege') -and -not $yes) { $yes = $true; continue }
        if ($arg -ceq '--accept-system-management' -and $action -ceq 'privilege' -and -not $accept) { $accept = $true; continue }
        if ($arg -ceq '--version' -and $action -ceq 'update' -and -not $version -and $index -lt $Commands.Count) {
            $version = $Commands[$index]; $index++
            if ($version -cnotmatch '\Av(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\z') { throw '版本必须为稳定版 v主版本.次版本.修订号。' }
            continue
        }
        if ($arg -ceq '--lines' -and $action -ceq 'logs' -and $index -lt $Commands.Count -and $Commands.Count -eq 3) {
            $text = $Commands[$index]; $index++
            if ($text -cnotmatch '\A[1-9][0-9]{0,2}\z' -or [int]$text -gt 200) { throw '日志行数必须为 1–200，默认 50。' }
            $lines = [int]$text; continue
        }
        throw '维护命令参数不匹配或重复；请输入 qqon help。'
    }
    if ($action -ceq '--help') { $action = 'help' }
    return @{ Action = $action; Yes = $yes; Version = $version; Lines = $lines; AcceptSystemManagement = $accept; Text = ($Commands -join ' ') }
}

function Test-QPWrapperJsonValue {
    param([hashtable]$State, [int]$Depth = 0)
    if ($Depth -gt 3) { throw '安装配置 JSON 嵌套过深。' }
    while ($State.Pos -lt $State.Text.Length -and $State.Text[$State.Pos] -cin @([char]' ', [char]9, [char]10, [char]13)) { $State.Pos++ }
    if ($State.Pos -ge $State.Text.Length) { throw '安装配置 JSON 不完整。' }
    if ($State.Text[$State.Pos] -ceq '{') {
        $State.Pos++; $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
        while ($true) {
            while ($State.Pos -lt $State.Text.Length -and [char]::IsWhiteSpace($State.Text[$State.Pos])) { $State.Pos++ }
            $key = [regex]::Match($State.Text.Substring($State.Pos), '\A"([A-Za-z][A-Za-z0-9]*)"\s*:')
            if (-not $key.Success -or -not $seen.Add($key.Groups[1].Value)) { throw '安装配置含转义、重复或无效字段。' }
            $State.Pos += $key.Length; Test-QPWrapperJsonValue $State ($Depth + 1)
            while ($State.Pos -lt $State.Text.Length -and [char]::IsWhiteSpace($State.Text[$State.Pos])) { $State.Pos++ }
            if ($State.Pos -ge $State.Text.Length) { throw '安装配置 JSON 不完整。' }
            $next = $State.Text[$State.Pos]; $State.Pos++
            if ($next -ceq '}') { return }
            if ($next -cne ',') { throw '安装配置 JSON 分隔符无效。' }
        }
    }
    $token = [regex]::Match($State.Text.Substring($State.Pos), '\A(?:"(?:[^"\\\x00-\x1f]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*"|null|true|false|-?(?:0|[1-9][0-9]*))')
    if (-not $token.Success) { throw '安装配置 JSON 值无效。' }
    $State.Pos += $token.Length
}

function Read-QPWrapperIdentity {
    param([byte[]]$Bytes)
    if ($null -eq $Bytes -or $Bytes.Length -lt 1 -or $Bytes.Length -gt 32768) { throw '已安装配置大小无效。' }
    $text = [Text.UTF8Encoding]::new($false, $true).GetString($Bytes)
    if ($text.Length -eq 0 -or $text[0] -ceq [char]0xfeff) { throw '安装配置必须是无 BOM 的 UTF-8 JSON。' }
    $state = @{ Text = $text; Pos = 0 }; Test-QPWrapperJsonValue $state
    if ($text.Substring($state.Pos) -cnotmatch '\A[ \t\r\n]*\z') { throw '安装配置含额外 JSON 或尾随内容。' }
    $value = ConvertFrom-Json -InputObject $text -ErrorAction Stop
    $fields = @('format', 'repository', 'service', 'listen', 'origin', 'healthHost', 'version', 'dataSchema', 'commit', 'buildTime', 'binarySize', 'binarySHA256', 'installerSHA256', 'updaterSHA256', 'accessMode', 'tls')
    if ($value.format -eq 3) { $fields += @('privilegeMode', 'privilegeConsent') }
    if (@($value.PSObject.Properties).Count -ne $fields.Count) { throw '安装配置顶层字段不匹配。' }
    foreach ($field in $fields) { if ($field -cnotin @($value.PSObject.Properties.Name)) { throw '安装配置字段名称不正确。' } }
    if ($value.format -isnot [int] -or $value.format -notin @(2,3) -or $value.repository -cne 'LinyuFiee/QQON-Panel' -or $value.service -cne 'QQONPanel' -or $value.dataSchema -ne 1 -or $value.installerSHA256 -isnot [string] -or $value.updaterSHA256 -isnot [string] -or $value.installerSHA256 -cnotmatch '\A[0-9a-f]{64}\z' -or $value.updaterSHA256 -cnotmatch '\A[0-9a-f]{64}\z') { throw '已安装管理器身份无效；旧版只能通过已校验安装器单独迁移。' }
    if ($null -ne $value.tls) {
        $tlsFields = @('generation', 'host', 'fingerprint', 'notAfter')
        if (@($value.tls.PSObject.Properties).Count -ne 4) { throw 'TLS 配置字段不匹配。' }
        foreach ($field in $tlsFields) { if ($field -cnotin @($value.tls.PSObject.Properties.Name)) { throw 'TLS 配置字段名不正确。' } }
    }
    if ($value.format -eq 3) {
        if ($value.privilegeMode -cnotin @('restricted', 'system')) { throw '安装配置权限模式无效。' }
        if ($value.privilegeMode -ceq 'restricted') { if ($null -ne $value.privilegeConsent) { throw '受限模式不得声明最高权限同意。' } }
        else {
            $consent = $value.privilegeConsent; $consentFields = @('format','mode','identity','noticeVersion','acceptedAt','acceptedBy','notice')
            if ($null -eq $consent -or @($consent.PSObject.Properties).Count -ne 7) { throw '最高权限同意字段缺失。' }
            foreach ($field in $consentFields) { if ($field -cnotin @($consent.PSObject.Properties.Name)) { throw '最高权限同意字段无效。' } }
            if ($consent.format -isnot [int] -or $consent.format -ne 1 -or $consent.mode -cne 'system' -or $consent.identity -cne 'LocalSystem' -or $consent.noticeVersion -isnot [int] -or $consent.noticeVersion -ne 1 -or $consent.acceptedBy -cne 'Administrator' -or $consent.notice -cne 'full-system-management' -or $consent.acceptedAt -isnot [string] -or $consent.acceptedAt -cnotmatch '\A[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\z') { throw '最高权限同意内容无效。' }
            $time = [datetimeoffset]::MinValue
            if (-not [datetimeoffset]::TryParse($consent.acceptedAt, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal, [ref]$time) -or $time -gt [datetimeoffset]::UtcNow.AddMinutes(5)) { throw '最高权限同意时间无效。' }
        }
    }
    return $value
}

function Invoke-QPInstalledManager {
    param([string[]]$Commands)
    $command = Read-QPWrapperCommand $Commands
    if ($PSVersionTable.PSEdition -cne 'Desktop' -or $PSVersionTable.PSVersion -lt [version]'5.1' -or -not [Environment]::Is64BitProcess) { throw '请使用原生 64 位 Windows PowerShell 5.1。' }
    $paths = Get-QPWrapperPaths
    Assert-QPWrapperPath $paths.Code
    Assert-QPWrapperPath $paths.State -State
    Assert-QPWrapperPath $paths.Installer -File
    Assert-QPWrapperPath $paths.Updater -File
    Assert-QPWrapperPath $paths.Config -File -AdminOnly
    $stream = [IO.FileStream]::new($paths.Config, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        if ($stream.Length -lt 1 -or $stream.Length -gt 32768) { throw '安装配置文件超出长度限制。' }
        $bytes = New-Object byte[] ([int]$stream.Length); $offset = 0
        while ($offset -lt $bytes.Length) { $n = $stream.Read($bytes, $offset, $bytes.Length - $offset); if ($n -lt 1) { throw '安装配置读取不完整。' }; $offset += $n }
        if ($stream.ReadByte() -ne -1) { throw '安装配置在读取期间发生变化。' }
    } finally { $stream.Dispose() }
    $value = Read-QPWrapperIdentity $bytes
    if ((Get-QPWrapperHash $paths.Installer 524288) -cne $value.installerSHA256 -or (Get-QPWrapperHash $paths.Updater 32768) -cne $value.updaterSHA256) { throw '管理器或更新入口摘要变化，已拒绝执行；请使用已校验安装器检查恢复。' }
    $powershell = [IO.Path]::Combine([Environment]::SystemDirectory, 'WindowsPowerShell\v1.0\powershell.exe')
    $arguments = @('-NoLogo', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $paths.Installer, '-Maintenance', $command.Text)
    Push-Location -LiteralPath ([Environment]::SystemDirectory)
    try { & $powershell @arguments; if ($LASTEXITCODE -ne 0) { throw 'QQON-Panel 维护未成功，请查看中文错误及保留的事务日志。' } }
    finally { Pop-Location }
}
# END TRUSTED WRAPPER

function Invoke-QPInstalledUpdater {
    param([string]$Version, [bool]$Yes)
    $commands = @('update')
    if ($Version) { $commands += @('--version', $Version) }; if ($Yes) { $commands += '--yes' }
    Invoke-QPInstalledManager $commands
}

if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference = 'Stop'
    try { Invoke-QPInstalledUpdater $Version ([bool]$Yes) }
    catch { [Console]::Error.WriteLine('QQON-Panel 更新入口未完成：' + $_.Exception.Message); exit 1 }
}
