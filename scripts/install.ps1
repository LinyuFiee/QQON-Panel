# QQON-Panel Windows setup manager. PowerShell 5.1 / inbox .NET only.
[CmdletBinding()]
param(
    [ValidateSet('Install', 'Update', 'Status')][string]$Action,
    [int]$Port = 5173,
    [string]$Origin = '',
    [string]$Version = '',
    [switch]$Yes
)

# BEGIN SHARED RELEASE READER (kept byte-identical to bootstrap.ps1 by tests)
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
    param([ValidateSet('Code', 'State', 'Data', 'Admin')][string]$Kind)
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
    param([ValidateSet('Code', 'State', 'Data', 'Admin')][string]$Kind, [switch]$File)
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
    param([string]$Path, [ValidateSet('Code', 'State', 'Data', 'Admin')][string]$Kind, [switch]$File, [switch]$Root)
    Assert-QPPlainPath $Path
    Assert-QP (($File -and [IO.File]::Exists($Path)) -or (-not $File -and [IO.Directory]::Exists($Path))) 'Expected regular file or directory.'
    if ($File) { $acl = [IO.File]::GetAccessControl($Path) } else { $acl = [IO.Directory]::GetAccessControl($Path) }
    $owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
    $owners = @('S-1-5-18', 'S-1-5-32-544'); if ($Kind -eq 'Data' -and -not $Root) { $owners += (Get-QPServiceSid).Value }
    Assert-QP ($owner -in $owners) ('Unsafe owner on ' + $Path)
    if ($Root) { Assert-QP $acl.AreAccessRulesProtected ('Unprotected directory ACL: ' + $Path) }
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
    param([string]$Path, [ValidateSet('Code', 'State', 'Data', 'Admin')][string]$Kind)
    Assert-QPPlainPath $Path -AllowMissing
    Assert-QP (-not [IO.Directory]::Exists($Path) -and -not [IO.File]::Exists($Path)) ('Refusing preexisting installation path: ' + $Path)
    [IO.DirectoryInfo]::new($Path).Create((New-QPSecurity $Kind))
    Assert-QPAcl $Path $Kind -Root
}

function Read-QPFile {
    param([string]$Path, [int]$Limit)
    Assert-QPPlainPath $Path
    $stream = [IO.FileStream]::new($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
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
namespace QQONPanel {
  public static class NativeProcess {
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

function Read-QPConfigValue {
    param([hashtable]$Value)
    Assert-QPFields $Value @('format', 'repository', 'service', 'listen', 'origin', 'healthHost', 'version', 'dataSchema', 'commit', 'buildTime', 'binarySize', 'binarySHA256', 'installerSHA256', 'updaterSHA256')
    Assert-QP (Test-QPInteger $Value['format'] 1 1) 'Unsupported Windows installation format.'
    Assert-QPIdentity $Value
    Assert-QP ($Value['service'] -is [string] -and $Value['service'] -ceq 'QQONPanel') 'Unknown Windows service identity.'
    Assert-QP ($Value['listen'] -is [string] -and $Value['listen'] -cmatch '\A127\.0\.0\.1:([1-9][0-9]{3,4})\z') 'Listener must remain IPv4 loopback.'
    $port = [int]($Value['listen'].Split([char]':')[1])
    Assert-QP ($Value['origin'] -is [string] -and $Value['healthHost'] -is [string]) 'Invalid origin/health host types.'
    $originInfo = Get-QPOrigin $port $Value['origin']
    Assert-QP ($originInfo.Origin -ceq $Value['origin'] -and $originInfo.Host -ceq $Value['healthHost']) 'Origin and health Host are not normalized or do not agree.'
    Assert-QP (Test-QPInteger $Value['binarySize'] 1 67108864) 'Invalid installed binary size.'
    foreach ($key in @('binarySHA256', 'installerSHA256', 'updaterSHA256')) { Assert-QP ($Value[$key] -is [string] -and $Value[$key] -cmatch '\A[0-9a-f]{64}\z') 'Invalid installation digest.' }
    return ,$Value
}

function Read-QPConfig {
    param([hashtable]$Paths)
    Assert-QPAcl $Paths.Config 'Admin' -File
    return Read-QPConfigValue (ConvertFrom-QPJson (Read-QPFile $Paths.Config 32768))
}

function Read-QPTransaction {
    param([hashtable]$Paths)
    Assert-QPAcl $Paths.Journal 'Admin' -File
    $value = ConvertFrom-QPJson (Read-QPFile $Paths.Journal 32768)
    Assert-QPFields $value @('format', 'kind', 'phase', 'old', 'new')
    Assert-QP ((Test-QPInteger $value['format'] 1 1) -and $value['kind'] -is [string] -and $value['kind'] -cin @('install', 'update') -and $value['phase'] -is [string] -and $value['phase'] -cin @('prepared', 'ready')) 'Unsupported transaction journal.'
    $new = Read-QPConfigValue $value['new']
    if ($value['kind'] -ceq 'install') { Assert-QP ($null -eq $value['old']) 'Installation journal must not contain previous state.' }
    else {
        $old = Read-QPConfigValue $value['old']
        foreach ($key in @('format', 'repository', 'service', 'listen', 'origin', 'healthHost', 'dataSchema', 'installerSHA256', 'updaterSHA256')) { Assert-QP ($old[$key] -ceq $new[$key]) 'Update journal changes fixed install settings or manager.' }
        Assert-QP ((ConvertTo-QPVersion $new['version']) -gt (ConvertTo-QPVersion $old['version']) -and $old['binarySHA256'] -cne $new['binarySHA256']) 'Journal update is not a strict version/digest upgrade.'
    }
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

function Get-QPServiceCommand {
    param([hashtable]$Paths, [hashtable]$Config)
    $command = (ConvertTo-QPArgument $Paths.Binary) + ' -listen ' + (ConvertTo-QPArgument $Config['listen']) + ' -data-dir ' + (ConvertTo-QPArgument $Paths.Data)
    if ($Config['origin']) { $command += ' -origin ' + (ConvertTo-QPArgument $Config['origin']) }
    return $command + ' -github-repo LinyuFiee/QQON-Panel'
}

function Assert-QPService {
    param([hashtable]$Paths, [hashtable]$Config, $Service)
    Assert-QP ($null -ne $Service) 'Managed QQONPanel service is missing; manual installations are not adopted.'
    Assert-QP ($Service.Name -ceq 'QQONPanel' -and $Service.DisplayName -ceq 'QQON-Panel' -and $Service.StartName -ieq 'NT SERVICE\QQONPanel' -and $Service.ServiceType -eq 'Own Process' -and $Service.StartMode -eq 'Auto' -and $Service.PathName -ceq (Get-QPServiceCommand $Paths $Config)) 'Service account, startup type, SCM executable or arguments do not match managed metadata.'
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
    [void](Invoke-QPProcess $sc @('create', 'QQONPanel', 'binPath=', (Get-QPServiceCommand $Paths $Config), 'type=', 'own', 'start=', 'auto', 'error=', 'normal', 'obj=', 'NT SERVICE\QQONPanel', 'DisplayName=', 'QQON-Panel'))
    [void](Invoke-QPProcess $sc @('sidtype', 'QQONPanel', 'unrestricted'))
    $translated = [Security.Principal.NTAccount]::new('NT SERVICE\QQONPanel').Translate([Security.Principal.SecurityIdentifier])
    Assert-QP ($translated.Value -ceq (Get-QPServiceSid).Value) 'Windows virtual-service SID does not match the expected identity.'
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
        } catch { throw 'Cannot prove panel.exe process identity; refusing a file switch.' }
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
        while ($clock.Elapsed.TotalSeconds -lt 45) {
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
        throw 'Service/process/file unlock did not complete within 45 seconds. No force-kill or delete-lock workaround is permitted.'
    } finally { $controller.Dispose() }
}

function Start-QPService {
    param([hashtable]$Paths, [hashtable]$Config)
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
    throw 'QQONPanel did not become healthy with the exact version/schema/Host within 30 seconds.'
}

function Assert-QPLayout {
    param([hashtable]$Paths)
    Assert-QPAcl $Paths.Code 'Code' -Root
    Assert-QPAcl $Paths.State 'State' -Root
    Assert-QPAcl $Paths.Data 'Data' -Root
    $allowed = @('panel.exe', 'panel.previous.exe', 'panel.next.exe', 'install.ps1', 'update.ps1')
    foreach ($entry in [IO.Directory]::EnumerateFileSystemEntries($Paths.Code)) {
        Assert-QP ([IO.Path]::GetFileName($entry) -cin $allowed) 'Unknown file in managed code directory; no automatic adoption or deletion.'
        Assert-QPAcl $entry 'Code' -File
    }
    # Inherit normally so runtime-created data is private to the virtual identity.
    # No automatic ACL repair: existing roots or data files must already be safe.
    $pending = [Collections.Generic.Stack[string]]::new()
    $pending.Push($Paths.Data); $count = 0; $clock = [Diagnostics.Stopwatch]::StartNew()
    while ($pending.Count -gt 0) {
        $parent = $pending.Pop()
        foreach ($entry in [IO.Directory]::EnumerateFileSystemEntries($parent)) {
            $count++; Assert-QP ($count -le 100000 -and $clock.Elapsed.TotalSeconds -lt 30) 'Private data validation limit exceeded; refusing without changing ACLs.'
            Assert-QPPlainPath $entry
            $isDirectory = ([IO.File]::GetAttributes($entry) -band [IO.FileAttributes]::Directory) -ne 0
            Assert-QPAcl $entry 'Data' -File:(-not $isDirectory)
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
    $transaction = @{ format = 1; kind = 'update'; phase = 'prepared'; old = $Old; new = $New }
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
        $reason = $_.Exception.Message
        try { Undo-QPUpdate $Paths $transaction }
        catch { throw ('Update failed; rollback is incomplete and the journal is retained. Data was NOT reset. Update error: ' + $reason + ' Rollback error: ' + $_.Exception.Message) }
        throw ('Update failed; previous version was atomically restored and passed health checks. Data was NOT reset. Cause: ' + $reason)
    }
}

function Resume-QPTransaction {
    param([hashtable]$Paths)
    $transaction = Read-QPTransaction $Paths
    Assert-QPLayout $Paths
    Assert-QPManagers $Paths $transaction['new']
    if ($transaction['kind'] -ceq 'update') {
        Assert-QPService $Paths $transaction['old'] (Get-QPService)
        Undo-QPUpdate $Paths $transaction
        [Console]::WriteLine('Interrupted update rolled back; previous version is healthy. Data preserved. Run Update again for a new release.')
        return
    }
    # Resume only our durable first-install journal, never an unknown manual directory/service.
    $config = $transaction['new']
    if (-not [IO.File]::Exists($Paths.Binary)) { Assert-QPBinary $Paths.Candidate $config; [IO.File]::Move($Paths.Candidate, $Paths.Binary) }
    Assert-QPBinary $Paths.Binary $config
    if ($null -eq (Get-QPService)) { Register-QPService $Paths $config }
    Assert-QPService $Paths $config (Get-QPService)
    Start-QPService $Paths $config
    Wait-QPHealthy $Paths $config
    Write-QPAtomicJson $Paths.Config $config
    Remove-QPManagedFile $Paths.Journal 'Admin'
    [Console]::WriteLine('Interrupted installation completed; QQONPanel is healthy. Existing data was preserved.')
}

function Get-QPWindowsRecord {
    param([hashtable]$Metadata)
    $records = @($Metadata.Manifest['binaries'] | Where-Object { $_['os'] -ceq 'windows' -and $_['arch'] -ceq 'amd64' })
    Assert-QP ($records.Count -eq 1) 'Exactly one Windows amd64 binary is required.'
    return ,$records[0]
}

function New-QPInstallConfig {
    param([hashtable]$Metadata, [int]$Port, [string]$Origin)
    $info = Get-QPOrigin $Port $Origin; $binary = Get-QPWindowsRecord $Metadata
    $value = @{
        format = 1; repository = 'LinyuFiee/QQON-Panel'; service = 'QQONPanel'; listen = '127.0.0.1:' + $Port
        origin = $info.Origin; healthHost = $info.Host; version = $Metadata.Manifest['version']; dataSchema = 1
        commit = $Metadata.Manifest['commit']; buildTime = $Metadata.Manifest['buildTime']; binarySize = $binary['size']
        binarySHA256 = $binary['sha256']; installerSHA256 = $Metadata.Sums['install.ps1']; updaterSHA256 = $Metadata.Sums['update.ps1']
    }
    return Read-QPConfigValue $value
}

function Install-QPPanel {
    param([hashtable]$Paths, [int]$Port, [string]$Origin, [string]$Version, [string]$Source)
    foreach ($path in @($Paths.Code, $Paths.State)) {
        Assert-QPPlainPath $path -AllowMissing
        Assert-QP (-not [IO.Directory]::Exists($path) -and -not [IO.File]::Exists($path)) 'Installation directory already exists. Use Status/Update for managed installations; unknown/manual paths are never adopted.'
        Assert-QPInstallParent ([IO.Path]::GetDirectoryName($path))
    }
    Assert-QP ($null -eq (Get-QPService)) 'QQONPanel service already exists; refusing adoption.'
    $metadata = Get-QPReleaseMetadata $Version
    $installer = Read-QPVerifiedAsset $metadata 'install.ps1'
    Assert-QP ((Get-QPFileHash $Source 524288) -ceq $metadata.Sums['install.ps1']) 'Run the reviewed installer from this selected Release; source does not match its checksum.'
    $updater = Read-QPVerifiedAsset $metadata 'update.ps1'
    $archive = Read-QPVerifiedAsset $metadata 'panel-windows-amd64.zip'
    $config = New-QPInstallConfig $metadata $Port $Origin
    $journaled = $false
    try {
        New-QPPrivateDirectory $Paths.Code 'Code'
        New-QPPrivateDirectory $Paths.State 'State'
        New-QPPrivateDirectory $Paths.Data 'Data'
        Write-QPNewFile $Paths.Installer $installer 'Code'
        Write-QPNewFile $Paths.Updater $updater 'Code'
        Expand-QPWindowsZip $archive (Get-QPWindowsRecord $metadata) $Paths.Candidate
        Assert-QPBinary $Paths.Candidate $config
        $transaction = @{ format = 1; kind = 'install'; phase = 'prepared'; old = $null; new = $config }
        Write-QPAtomicJson $Paths.Journal $transaction; $journaled = $true
        [IO.File]::Move($Paths.Candidate, $Paths.Binary)
        Register-QPService $Paths $config
        $transaction['phase'] = 'ready'; Write-QPAtomicJson $Paths.Journal $transaction
        Start-QPService $Paths $config
        Wait-QPHealthy $Paths $config
        Write-QPAtomicJson $Paths.Config $config
        Remove-QPManagedFile $Paths.Journal 'Admin'
    } catch {
        $reason = $_.Exception.Message
        if ($journaled) {
            try { if ($null -ne (Get-QPService)) { Stop-QPServiceAndWait $Paths $config } } catch { $reason += ' Stop also failed: ' + $_.Exception.Message }
            throw ('Installation incomplete. Journal/files/data retained; run Update to recover after resolving the error. No fake success. ' + $reason)
        }
        # Even pre-journal failures never recursively delete a data directory or adopt it later.
        throw ('Installation failed before service registration. Any partial private directories are retained for administrator inspection; data is never erased. ' + $reason)
    }
    [Console]::WriteLine('Installed QQON-Panel ' + $config['version'] + '; SCM QQONPanel is healthy under NT SERVICE\QQONPanel.')
    [Console]::WriteLine('Loopback only: http://' + $config['listen'] + '/  Data: ' + $Paths.Data)
}

function Update-QPPanel {
    param([hashtable]$Paths, [string]$Version)
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
        [Console]::WriteLine('Already at ' + $old['version'] + '; no update performed.'); return
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
        Assert-QPBinary $Paths.Candidate $new
        Invoke-QPUpdateTransaction $Paths $old $new
    } finally {
        if (-not [IO.File]::Exists($Paths.Journal)) { Remove-QPManagedFile $Paths.Candidate 'Code' }
    }
    [Console]::WriteLine('Updated to ' + $new['version'] + '; SCM service passed exact version/schema health checks. Data was preserved.')
}

function Show-QPStatus {
    param([hashtable]$Paths)
    # Intentionally no locks, recovery, network release lookup, process execution or ACL writes.
    $codeExists = [IO.Directory]::Exists($Paths.Code); $stateExists = [IO.Directory]::Exists($Paths.State)
    $service = Get-QPService
    if (-not $codeExists -and -not $stateExists -and $null -eq $service) { [Console]::WriteLine('QQON-Panel is not installed.'); return }
    Assert-QPLayout $Paths
    if ([IO.File]::Exists($Paths.Journal)) {
        $journal = Read-QPTransaction $Paths
        [Console]::WriteLine('Incomplete ' + $journal['kind'] + ' transaction (' + $journal['phase'] + '). Status did not recover or change anything; use Update for recovery.')
        return
    }
    $config = Read-QPConfig $Paths
    Assert-QPManagers $Paths $config; Assert-QPBinary $Paths.Binary $config -ReadOnly
    Assert-QPService $Paths $config $service
    [Console]::WriteLine('QQON-Panel ' + $config['version'] + ' | SCM: ' + $service.State + ' | account: ' + $service.StartName)
    [Console]::WriteLine('Listen: ' + $config['listen'] + ' | origin: ' + $config['origin'] + ' | data: ' + $Paths.Data)
    [Console]::WriteLine('Code hashes, private ACLs and SCM configuration validated. Status did not start/stop or execute the binary.')
}

function Read-QPConsole {
    param([string]$Prompt)
    Assert-QP (-not [Console]::IsInputRedirected) 'Interactive setup needs a real console. Use -Action Install|Update|Status and -Yes for changes.'
    [Console]::Write($Prompt)
    $value = [Console]::ReadLine()
    Assert-QP ($null -ne $value) 'Console input ended.'
    return $value
}

function Invoke-QPInstaller {
    param([hashtable]$Options, [string]$Source)
    $action = [string]$Options['Action']
    if (-not $action) {
        while ($true) {
            [Console]::WriteLine("QQON-Panel  1 Install  2 Update  3 Status  0 Exit")
            switch ((Read-QPConsole 'Select: ').Trim()) {
                '1' { $action = 'Install' }
                '2' { $action = 'Update' }
                '3' { $action = 'Status' }
                '0' { return }
                default { [Console]::WriteLine('Choose 1, 2, 3 or 0.'); continue }
            }
            if ($action) { break }
        }
        if ($action -eq 'Install') {
            $text = Read-QPConsole 'Loopback port [5173, range 1024-65535]: '
            if ($text) { Assert-QP ($text -cmatch '\A[1-9][0-9]{3,4}\z') 'Invalid port.'; $Options['Port'] = [int]$text }
            $mode = Read-QPConsole 'Access: 1 local/SSH tunnel [default], 2 existing HTTPS reverse proxy: '
            Assert-QP ($mode -in @('', '1', '2')) 'Unsupported access mode.'
            if ($mode -eq '2') { $Options['Origin'] = Read-QPConsole 'Existing HTTPS domain origin (https://panel.example.com): ' }
        }
    }
    if ($action -eq 'Status') {
        Assert-QP (-not $Options['PortSpecified'] -and -not $Options['OriginSpecified']) 'Status accepts no mutating configuration options.'
        Assert-QPNativeHost
        Show-QPStatus (Get-QPPaths)
        return
    }
    Assert-QPNativeHost -Administrator
    Assert-QP ($action -in @('Install', 'Update')) 'Unsupported action.'
    if ($action -eq 'Update') { Assert-QP (-not $Options['PortSpecified'] -and -not $Options['OriginSpecified']) 'Update never changes listen/origin settings.' }
    if ($action -eq 'Install') { [void](Get-QPOrigin ([int]$Options['Port']) ([string]$Options['Origin'])) }
    if ($Options['Version']) { [void](ConvertTo-QPVersion $Options['Version'] -Tagged) }
    if (-not $Options['Yes']) {
        [Console]::WriteLine('This manages SCM QQONPanel, private ProgramFiles code and ProgramData data. It never resets existing data or changes the global execution policy.')
        Assert-QP ((Read-QPConsole ('Type YES to ' + $action + ': ')) -ceq 'YES') 'No changes confirmed.'
    }
    $paths = Get-QPPaths
    $mutex = Enter-QPLock
    try {
        if ($action -eq 'Install') { Install-QPPanel $paths ([int]$Options['Port']) ([string]$Options['Origin']) ([string]$Options['Version']) $Source }
        else { Update-QPPanel $paths ([string]$Options['Version']) }
    } finally { Exit-QPLock $mutex }
}

# Dot-source loads definitions only. Tests mock functions, never production path/env flags.
if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference = 'Stop'
    $options = @{ Action = $Action; Port = $Port; Origin = $Origin; Version = $Version; Yes = [bool]$Yes; PortSpecified = $PSBoundParameters.ContainsKey('Port'); OriginSpecified = $PSBoundParameters.ContainsKey('Origin') }
    try { Invoke-QPInstaller $options $PSCommandPath }
    catch { [Console]::Error.WriteLine('QQON-Panel setup refused/failed: ' + $_.Exception.Message); exit 1 }
}
