# QQON-Panel Windows entry. PowerShell 5.1 / inbox .NET only.
# The Pages copy of this file is an entry point, not a second installer.
[CmdletBinding()]
param()

function Assert-QP {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
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

function Invoke-QPBootstrap {
    Assert-QPNativeHost -Administrator
    [Console]::WriteLine('QQON-Panel: fetch only the fixed GitHub Release, verify its manifest and SHA-256, then execute a protected local file.')
    [Console]::WriteLine('TLS and checksums trust this repository/publisher; they are not an independent code signature. No global execution policy is changed.')
    $metadata = Get-QPReleaseMetadata
    $bytes = Read-QPVerifiedAsset $metadata 'install.ps1'
    # Do not execute elevated code under a user-owned TEMP ancestor. The inbox
    # Windows Temp parent allows creating children, but not replacing ours.
    $tempBase = [IO.Path]::Combine([IO.Directory]::GetParent([Environment]::SystemDirectory).FullName, 'Temp')
    Assert-QPPlainPath $tempBase
    $parentAcl = [IO.Directory]::GetAccessControl($tempBase)
    $trustedParents = @('S-1-5-18', 'S-1-5-32-544', 'S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464')
    Assert-QP ($parentAcl.GetOwner([Security.Principal.SecurityIdentifier]).Value -in $trustedParents) 'Windows Temp parent has an untrusted owner.'
    $dangerous = [long][Security.AccessControl.FileSystemRights]'Delete, DeleteSubdirectoriesAndFiles, ChangePermissions, TakeOwnership'
    foreach ($rule in $parentAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
        if ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $rule.IdentityReference.Value -notin $trustedParents -and ($rule.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) -eq 0) {
            Assert-QP (([long]$rule.FileSystemRights -band $dangerous) -eq 0) 'Windows Temp parent allows unprivileged replacement; refusing elevated download execution.'
        }
    }
    $directory = [IO.Path]::Combine($tempBase, 'QQON-Panel-bootstrap-' + [guid]::NewGuid().ToString('N'))
    $security = [Security.AccessControl.DirectorySecurity]::new()
    $security.SetAccessRuleProtection($true, $false)
    $admin = [Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
    $security.SetOwner($admin)
    foreach ($sid in @($admin, [Security.Principal.SecurityIdentifier]::new('S-1-5-18'))) {
        $security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, [Security.AccessControl.FileSystemRights]::FullControl, [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit', [Security.AccessControl.PropagationFlags]::None, [Security.AccessControl.AccessControlType]::Allow))
    }
    Assert-QP (-not [IO.Directory]::Exists($directory) -and -not [IO.File]::Exists($directory)) 'Bootstrap temporary path already exists.'
    [IO.DirectoryInfo]::new($directory).Create($security)
    $script = [IO.Path]::Combine($directory, 'install.ps1')
    try {
        Assert-QPPlainPath $directory
        $actual = [IO.Directory]::GetAccessControl($directory)
        Assert-QP ($actual.AreAccessRulesProtected -and $actual.GetOwner([Security.Principal.SecurityIdentifier]).Value -ceq $admin.Value) 'Unsafe bootstrap directory owner or DACL.'
        foreach ($rule in $actual.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
            Assert-QP ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $rule.IdentityReference.Value -in @('S-1-5-18', 'S-1-5-32-544')) 'Unsafe bootstrap directory access.'
        }
        $stream = [IO.FileStream]::new($script, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try { $stream.Write($bytes, 0, $bytes.Length); $stream.Flush($true) } finally { $stream.Dispose() }
        Assert-QP ((Get-QPSha256 ([IO.File]::ReadAllBytes($script))) -ceq $metadata.Sums['install.ps1']) 'Local bootstrap installer digest changed.'
        $start = [Diagnostics.ProcessStartInfo]::new()
        $start.FileName = [IO.Path]::Combine([Environment]::SystemDirectory, 'WindowsPowerShell\v1.0\powershell.exe')
        $start.WorkingDirectory = [Environment]::SystemDirectory
        $start.UseShellExecute = $false
        $start.Arguments = '-NoLogo -NoProfile -ExecutionPolicy Bypass -File ' + (ConvertTo-QPArgument $script) + ' -Version ' + (ConvertTo-QPArgument $metadata.Release.Tag)
        $process = [Diagnostics.Process]::Start($start)
        try { $process.WaitForExit(); Assert-QP ($process.ExitCode -eq 0) 'Windows installer failed; no success is being reported.' } finally { $process.Dispose() }
    } finally {
        # Delete only the file/directory created above, never recurse through downloaded content.
        if ([IO.File]::Exists($script)) { [IO.File]::Delete($script) }
        if ([IO.Directory]::Exists($directory)) { [IO.Directory]::Delete($directory, $false) }
    }
}

# Dot-sourcing is a definition-only test/import entry. No production path overrides.
if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference = 'Stop'
    try { Invoke-QPBootstrap } catch { throw ('QQON-Panel bootstrap refused/failed: ' + $_.Exception.Message) }
}
