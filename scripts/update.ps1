# QQON-Panel fixed installed updater. No remote script or global policy changes.
[CmdletBinding()]
param([string]$Version = '', [switch]$Yes)

function Assert-QPWrapperPath {
    param([string]$Path, [switch]$File, [switch]$AdminOnly)
    if ($Path -cnotmatch '\A[A-Za-z]:\\' -or [IO.Path]::GetFullPath($Path) -cne $Path) { throw 'Non-canonical installed updater path.' }
    $current = $Path.Substring(0, 3)
    foreach ($part in $Path.Substring(3).Split([char]'\')) {
        $current = [IO.Path]::Combine($current, $part)
        if (([IO.File]::GetAttributes($current) -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Updater paths cannot contain reparse points.' }
    }
    if ($File) { $acl = [IO.File]::GetAccessControl($Path) } else { $acl = [IO.Directory]::GetAccessControl($Path) }
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin @('S-1-5-18', 'S-1-5-32-544')) { throw 'Installed updater owner is not Administrator/SYSTEM.' }
    if (-not $File -and -not $acl.AreAccessRulesProtected) { throw 'Installed updater root must have a protected ACL.' }
    $serviceSid = ([Security.Principal.NTAccount]::new('NT SERVICE\QQONPanel').Translate([Security.Principal.SecurityIdentifier])).Value
    $seen = @{}
    foreach ($rule in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
        $sid = $rule.IdentityReference.Value
        if ($rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or $rule.PropagationFlags -ne [Security.AccessControl.PropagationFlags]::None) { throw 'Unexpected installed updater ACL.' }
        if ($sid -in @('S-1-5-18', 'S-1-5-32-544')) { $seen[$sid] = $true; continue }
        $max = [long][Security.AccessControl.FileSystemRights]'ReadAndExecute, Synchronize'
        if ($AdminOnly -or $sid -cne $serviceSid -or (([long]$rule.FileSystemRights -band (-bnot $max)) -ne 0)) { throw 'Installed updater ACL grants access outside Administrator/SYSTEM and read-only virtual service.' }
    }
    if (-not $seen.ContainsKey('S-1-5-18') -or -not $seen.ContainsKey('S-1-5-32-544')) { throw 'Required installed updater access is missing.' }
}

function Get-QPWrapperHash {
    param([string]$Path, [int]$Limit)
    $stream = [IO.FileStream]::new($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        if ($stream.Length -lt 1 -or $stream.Length -gt $Limit) { throw 'Installed updater file size is invalid.' }
        return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '').ToLowerInvariant()
    } finally { $sha.Dispose(); $stream.Dispose() }
}

function Invoke-QPInstalledUpdater {
    param([string]$Version, [bool]$Yes)
    if ($PSVersionTable.PSEdition -cne 'Desktop' -or $PSVersionTable.PSVersion -lt [version]'5.1' -or -not [Environment]::Is64BitProcess) { throw 'Use native 64-bit Windows PowerShell 5.1.' }
    if ($Version -and $Version -cnotmatch '\Av(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\z') { throw 'Version must be stable vMAJOR.MINOR.PATCH.' }
    $code = [IO.Path]::Combine([Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFiles), 'QQON-Panel')
    $state = [IO.Path]::Combine([Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData), 'QQON-Panel')
    $installer = [IO.Path]::Combine($code, 'install.ps1'); $wrapper = [IO.Path]::Combine($code, 'update.ps1')
    $config = [IO.Path]::Combine($state, 'install.json')
    Assert-QPWrapperPath $code
    Assert-QPWrapperPath $state
    Assert-QPWrapperPath $installer -File
    Assert-QPWrapperPath $wrapper -File
    Assert-QPWrapperPath $config -File -AdminOnly
    $info = [IO.FileInfo]::new($config)
    if ($info.Length -lt 1 -or $info.Length -gt 32768) { throw 'Installed metadata size is invalid.' }
    $bytes = [IO.File]::ReadAllBytes($config)
    if ($bytes.Length -ne $info.Length) { throw 'Installed metadata changed during read.' }
    $text = [Text.UTF8Encoding]::new($false, $true).GetString($bytes)
    # Generated Windows config has flat, literal ASCII field names; escaped/duplicate keys fail closed.
    $fields = @('format', 'repository', 'service', 'listen', 'origin', 'healthHost', 'version', 'dataSchema', 'commit', 'buildTime', 'binarySize', 'binarySHA256', 'installerSHA256', 'updaterSHA256')
    $keys = [regex]::Matches($text, '"((?:[^"\\]|\\.)*)"\s*:')
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    if ($keys.Count -ne $fields.Count) { throw 'Unexpected installed metadata fields.' }
    foreach ($key in $keys) {
        $name = $key.Groups[1].Value
        if ($name -cnotin $fields -or -not $seen.Add($name)) { throw 'Unknown, escaped or duplicate installed metadata key.' }
    }
    $value = ConvertFrom-Json -InputObject $text -ErrorAction Stop
    if ($value.format -isnot [int] -or $value.format -ne 1 -or $value.repository -cne 'LinyuFiee/QQON-Panel' -or $value.service -cne 'QQONPanel' -or $value.installerSHA256 -isnot [string] -or $value.updaterSHA256 -isnot [string] -or $value.installerSHA256 -cnotmatch '\A[0-9a-f]{64}\z' -or $value.updaterSHA256 -cnotmatch '\A[0-9a-f]{64}\z') { throw 'Invalid installed manager identity.' }
    if ((Get-QPWrapperHash $installer 524288) -cne $value.installerSHA256 -or (Get-QPWrapperHash $wrapper 32768) -cne $value.updaterSHA256) { throw 'Installed manager/wrapper hash changed. Refusing execution.' }
    $powershell = [IO.Path]::Combine([Environment]::SystemDirectory, 'WindowsPowerShell\v1.0\powershell.exe')
    $arguments = @('-NoLogo', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $installer, '-Action', 'Update')
    if ($Version) { $arguments += @('-Version', $Version) }; if ($Yes) { $arguments += '-Yes' }
    Push-Location -LiteralPath ([Environment]::SystemDirectory)
    try { & $powershell @arguments; if ($LASTEXITCODE -ne 0) { throw 'QQON-Panel update failed; consult the installer error/journal.' } }
    finally { Pop-Location }
}

if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference = 'Stop'
    try { Invoke-QPInstalledUpdater $Version ([bool]$Yes) }
    catch { [Console]::Error.WriteLine('QQON-Panel updater refused/failed: ' + $_.Exception.Message); exit 1 }
}
