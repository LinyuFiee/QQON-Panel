# Native PowerShell 5.1 tests. No Pester, network, service registration or live install paths.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
$script:Passed = 0
$script:Failed = 0
$script:Scripts = [IO.Path]::GetFullPath([IO.Path]::Combine($PSScriptRoot, '..'))
$script:Root = [IO.Path]::Combine([IO.Path]::GetTempPath(), 'QQON-windows-tests-' + [guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($script:Root)
$script:PolicyBefore = (Get-ExecutionPolicy -List | Out-String)

function Check {
    param([bool]$Condition, [string]$Message = 'Assertion failed')
    if (-not $Condition) { throw $Message }
}
function Refuses {
    param([scriptblock]$Body, [string]$Pattern = '.')
    $legacyPatterns = @{ nine = 'QP_RELEASE_ASSETS'; canonical = 'QP_HTTPS'; size = 'QP_LIMIT'; duplicate = 'QP_(SYSTEM|INTEGRITY)'; 'format 3' = 'QP_RELEASE_ASSETS'; differs = 'QP_IDENTITY'; fixed = '固定'; Version = '版本|白名单'; 'owner|protected' = '管理员|受保护'; 'previous version was atomically restored' = '旧版本.*恢复|原子恢复'; 'rollback is incomplete' = '回滚未完成' }
    if ($legacyPatterns.ContainsKey($Pattern)) { $Pattern = $legacyPatterns[$Pattern] }
    $caught = $false
    try { & $Body | Out-Null } catch { $caught = $true; Check ($_.Exception.Message -match $Pattern) ('Wrong refusal: ' + $_.Exception.Message) }
    Check $caught 'Expected refusal, but the operation succeeded.'
}
function Test {
    param([string]$Name, [scriptblock]$Body)
    try { & $Body; $script:Passed++; [Console]::WriteLine('PASS ' + $Name) }
    catch { $script:Failed++; [Console]::Error.WriteLine('FAIL ' + $Name + ': ' + $_.Exception.Message + "`n" + $_.ScriptStackTrace) }
}
function Bytes {
    param([string]$Text)
    return ,([Text.UTF8Encoding]::new($false).GetBytes($Text))
}
function Assert-TestPath {
    param([string]$Path)
    $full = [IO.Path]::GetFullPath($Path)
    Check ($full.StartsWith($script:Root + '\', [StringComparison]::OrdinalIgnoreCase)) ('Test attempted a non-temporary write: ' + $full)
    Assert-QPPlainPath $full -AllowMissing
}
function New-TestSecurity {
    param([switch]$File)
    if ($File) { $acl = [Security.AccessControl.FileSecurity]::new() } else { $acl = [Security.AccessControl.DirectorySecurity]::new() }
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl.SetOwner($sid); $acl.SetAccessRuleProtection($true, $false)
    $inherit = [Security.AccessControl.InheritanceFlags]::None
    if (-not $File) { $inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit' }
    foreach ($id in @($sid, [Security.Principal.SecurityIdentifier]::new('S-1-5-18'))) {
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($id, [Security.AccessControl.FileSystemRights]::FullControl, $inherit, [Security.AccessControl.PropagationFlags]::None, [Security.AccessControl.AccessControlType]::Allow))
    }
    return $acl
}
function New-TestPaths {
    $base = [IO.Path]::Combine($script:Root, [guid]::NewGuid().ToString('N'))
    [void][IO.Directory]::CreateDirectory($base)
    $code = [IO.Path]::Combine($base, 'code'); $state = [IO.Path]::Combine($base, 'state')
    [void][IO.Directory]::CreateDirectory($code); [void][IO.Directory]::CreateDirectory($state)
    $data = [IO.Path]::Combine($state, 'data'); [void][IO.Directory]::CreateDirectory($data)
    return @{ Code = $code; State = $state; Data = $data; Binary = "$code\panel.exe"; Candidate = "$code\panel.next.exe"; Previous = "$code\panel.previous.exe"; Installer = "$code\install.ps1"; Updater = "$code\update.ps1"; Config = "$state\install.json"; Journal = "$state\transaction.json" }
}
function New-Fixture {
    param([string]$Version = '0.6.0', [switch]$Legacy)
    $limits = Get-QPAssetLimits -Legacy:$Legacy
    $sums = @{}; $sizes = @{}; $assets = @()
    foreach ($name in $limits.Keys) {
        $sizes[$name] = 256
        if ($name -cne 'SHA256SUMS') { $sums[$name] = 'a' * 64 }
        $assets += @{ name = $name; size = 256; state = 'uploaded'; browser_download_url = Get-QPAssetUrl ('v' + $Version) $name }
    }
    $binaries = @()
    foreach ($platform in @('linux/amd64', 'linux/arm64', 'windows/amd64')) {
        if ($Legacy -and $platform.StartsWith('windows')) { continue }
        $os, $arch = $platform.Split([char]'/')
        $inner = 'panel'; $archive = 'panel-linux-' + $arch + '.tar.gz'
        if ($os -ceq 'windows') { $inner = 'panel.exe'; $archive = 'panel-windows-amd64.zip' }
        $binaries += @{ os = $os; arch = $arch; name = $inner; size = 512; sha256 = 'b' * 64; archive = @{ name = $archive; size = 256; sha256 = 'a' * 64 } }
    }
    $format = 3; if ($Legacy) { $format = 2 }
    $manifest = @{ format = $format; version = $Version; tag = 'v' + $Version; repository = 'LinyuFiee/QQON-Panel'; dataSchema = 1; commit = 'c' * 40; buildTime = '2026-11-07T00:00:00Z'; binaries = $binaries }
    $raw = ConvertTo-QPJsonBytes $manifest
    $sums['release.json'] = Get-QPSha256 $raw
    $release = @{ Tag = 'v' + $Version; Sizes = $sizes; Legacy = [bool]$Legacy }
    $api = @{ tag_name = 'v' + $Version; draft = $false; prerelease = $false; html_url = 'https://github.com/LinyuFiee/QQON-Panel/releases/tag/v' + $Version; assets = $assets }
    return @{ Manifest = $manifest; Raw = $raw; Sums = $sums; Release = $release; Api = $api }
}
function New-TestConfig {
    param([string]$Version = '0.6.0', [byte[]]$Payload)
    $value = New-QPInstallConfig (New-Fixture $Version) 5173 ''
    if ($null -ne $Payload) { $value['binarySize'] = $Payload.Length; $value['binarySHA256'] = Get-QPSha256 $Payload }
    return $value
}
function New-TestZip {
    param([byte[]]$Payload)
    Add-Type -AssemblyName System.IO.Compression
    $memory = [IO.MemoryStream]::new()
    $zip = [IO.Compression.ZipArchive]::new($memory, [IO.Compression.ZipArchiveMode]::Create, $true)
    try {
        $entry = $zip.CreateEntry('panel.exe', [IO.Compression.CompressionLevel]::Optimal)
        $entry.LastWriteTime = [datetimeoffset]::new(1980, 1, 1, 0, 0, 0, [timespan]::Zero)
        $entry.ExternalAttributes = -2115174400 # S_IFREG | 0755 in upper 16 bits
        $stream = $entry.Open(); try { $stream.Write($Payload, 0, $Payload.Length) } finally { $stream.Dispose() }
    } finally { $zip.Dispose() }
    $bytes = $memory.ToArray(); $memory.Dispose()
    $central = [int][BitConverter]::ToUInt32($bytes, $bytes.Length - 6)
    $bytes[$central + 5] = 3 # Unix creator, like the publisher's ZipInfo
    return ,$bytes
}
function Expected-Zip {
    param([byte[]]$Zip, [byte[]]$Payload)
    return @{ size = $Payload.Length; sha256 = Get-QPSha256 $Payload; archive = @{ size = $Zip.Length; sha256 = Get-QPSha256 $Zip } }
}

try {
    Test 'dot-source defines functions without host effects' {
        function Invoke-QPBootstrap { throw 'Unexpected bootstrap execution' }
        . ([IO.Path]::Combine($script:Scripts, 'bootstrap.ps1'))
        . ([IO.Path]::Combine($script:Scripts, 'install.ps1'))
        . ([IO.Path]::Combine($script:Scripts, 'update.ps1'))
        Check ($null -ne (Get-Command Read-QPManifest))
        Check ((Get-ExecutionPolicy -List | Out-String) -ceq $script:PolicyBefore)
    }
    . ([IO.Path]::Combine($script:Scripts, 'bootstrap.ps1'))
    . ([IO.Path]::Combine($script:Scripts, 'install.ps1'))
    . ([IO.Path]::Combine($script:Scripts, 'update.ps1'))

    Test 'standalone readers are identical, scripts parse, size limits' {
        $bootstrap = [IO.File]::ReadAllText([IO.Path]::Combine($script:Scripts, 'bootstrap.ps1'))
        $install = [IO.File]::ReadAllText([IO.Path]::Combine($script:Scripts, 'install.ps1'))
        $shared = $bootstrap.Substring($bootstrap.IndexOf('function Assert-QP {'), $bootstrap.IndexOf('function Invoke-QPBootstrap {') - $bootstrap.IndexOf('function Assert-QP {'))
        $start = $install.IndexOf('function Assert-QP {'); $end = $install.IndexOf('# END SHARED RELEASE READER')
        Check ($shared -ceq $install.Substring($start, $end - $start)) 'Bootstrap/installer parser drift.'
        foreach ($name in @('bootstrap.ps1', 'install.ps1', 'update.ps1')) {
            $path = [IO.Path]::Combine($script:Scripts, $name)
            $bytes = [IO.File]::ReadAllBytes($path)
            Check ($bytes[0] -eq 0xef -and $bytes[1] -eq 0xbb -and $bytes[2] -eq 0xbf) 'PowerShell 5.1 requires UTF-8 BOM for Chinese source.'
            Check (-not ([Text.Encoding]::UTF8.GetString($bytes)).Contains("`r")) 'Release source must use LF to keep source hashes reproducible.'
            $tokens = $null; $errors = $null; $ast = [Management.Automation.Language.Parser]::ParseFile($path, [ref]$tokens, [ref]$errors)
            Check ($errors.Count -eq 0) ($errors | Out-String)
            $limit = 524288; if ($name -ceq 'update.ps1') { $limit = 32768 }
            Check (([IO.FileInfo]::new($path)).Length -le $limit)
            Check (-not [IO.File]::ReadAllText($path).Contains('Set-ExecutionPolicy'))
            Check (@($ast.ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath }) -cnotcontains 'TestRoot')
        }
    }

    Test 'strict UTF-8 JSON rejects duplicate/case/escape/numeric tricks' {
        $good = ConvertFrom-QPJson (Bytes '{"n":1,"f":1.0,"b":false,"a":[],"s":"x\u0061","t":null}')
        Check ($good['n'] -is [long] -and $good['f'] -is [double] -and $good['b'] -is [bool] -and $good['a'] -is [object[]] -and $good['a'].Count -eq 0 -and $good['s'] -ceq 'xa' -and $null -eq $good['t'])
        foreach ($text in @('{"a":1,"a":2}', '{"a":1,"A":2}', '{"a":1,"\u0061":2}', '{"x":01}', '{"x":NaN}', '{"x":1e9999}', '{"x":1,}', '{} {}', '[]', '{"a":[[[[[[[[[0]]]]]]]]]}', '{"x":"\ud800"}')) { Refuses { ConvertFrom-QPJson (Bytes $text) } }
        Refuses { ConvertFrom-QPJson ([byte[]](0x7b,0x22,0x78,0x22,0x3a,0x22,0xc0,0xaf,0x22,0x7d)) }
        Refuses { ConvertFrom-QPJson ([byte[]](0xef,0xbb,0xbf,0x7b,0x7d)) }
        Refuses { ConvertFrom-QPJson (Bytes '{}') 1 }
    }

    Test 'format3 exact nine assets and canonical URLs' {
        $fixture = New-Fixture
        $release = Read-QPRelease (ConvertTo-QPJsonBytes $fixture.Api)
        Check ($release.Sizes.Count -eq 9)
        $parsed = Read-QPManifest $fixture.Raw $fixture.Sums $release
        Check ($parsed['binaries'].Count -eq 3)
        $fixture.Api['assets'] += @{ name = 'extra.txt'; size = 1; state = 'uploaded'; browser_download_url = 'https://example.com/' }
        Refuses { Read-QPRelease (ConvertTo-QPJsonBytes $fixture.Api) } 'nine'
        $fixture = New-Fixture; $fixture.Api['assets'][0]['browser_download_url'] = 'https://evil.example/x'
        Refuses { Read-QPRelease (ConvertTo-QPJsonBytes $fixture.Api) } 'canonical'
        $fixture = New-Fixture; $fixture.Api['assets'][0]['size'] = '256'
        Refuses { Read-QPRelease (ConvertTo-QPJsonBytes $fixture.Api) } 'size'
        $fixture = New-Fixture; $fixture.Api['assets'][0] = $fixture.Api['assets'][1]
        Refuses { Read-QPRelease (ConvertTo-QPJsonBytes $fixture.Api) } 'duplicate'
    }

    Test 'legacy format2 is informational only; bootstrap refuses pre-v0.5' {
        $fixture = New-Fixture '0.4.0' -Legacy
        Refuses { Read-QPRelease (ConvertTo-QPJsonBytes $fixture.Api) } 'v0.5.0'
        $release = Read-QPRelease (ConvertTo-QPJsonBytes $fixture.Api) -AllowFormat2
        $manifest = Read-QPManifest $fixture.Raw $fixture.Sums $release -AllowFormat2
        Check ($manifest['format'] -eq 2)
        Refuses { Read-QPManifest $fixture.Raw $fixture.Sums $release } 'format 3'
        function Assert-QPNativeHost { param([switch]$Administrator) }
        function Read-QPDownload { param($Url, $Limit, [switch]$Api) Check ([bool]$Api); return ,(ConvertTo-QPJsonBytes $fixture.Api) }
        Refuses { Invoke-QPBootstrap } 'v0.5.0'
    }

    Test 'manifest types, duplicate platforms, sizes and hashes fail closed' {
        foreach ($change in @(
            { param($m) $m['format'] = '3' }, { param($m) $m['dataSchema'] = '1' },
            { param($m) $m['commit'] = 'F' * 40 }, { param($m) $m['buildTime'] = '2026-02-30T00:00:00Z' },
            { param($m) $m['binaries'][2]['arch'] = 'arm64' }, { param($m) $m['binaries'][2]['name'] = '../panel.exe' },
            { param($m) $m['binaries'][2]['size'] = 67108865 }, { param($m) $m['binaries'][2]['archive']['size'] = 255 },
            { param($m) $m['binaries'][2]['archive']['sha256'] = 'd' * 64 }, { param($m) $m['tag'] = 'v0.5.1' }
        )) {
            $fixture = New-Fixture; & $change $fixture.Manifest
            $raw = ConvertTo-QPJsonBytes $fixture.Manifest; $fixture.Sums['release.json'] = Get-QPSha256 $raw
            Refuses { Read-QPManifest $raw $fixture.Sums $fixture.Release }
        }
        $fixture = New-Fixture
        Refuses { Read-QPManifest (Bytes '{}') $fixture.Sums $fixture.Release } 'SHA-256'
    }

    Test 'checksums exact8 LF ASCII and redirect boundaries' {
        $fixture = New-Fixture; $text = ''
        foreach ($name in ($fixture.Sums.Keys | Sort-Object)) { $text += $fixture.Sums[$name] + '  ' + $name + "`n" }
        Check ((Read-QPChecksums (Bytes $text)).Count -eq 8)
        Refuses { Read-QPChecksums (Bytes ($text + $text.Split([char]10)[0] + "`n")) } 'duplicate'
        Refuses { Read-QPChecksums (Bytes $text.Replace("`n", "`r`n")) }
        Refuses { Read-QPChecksums (Bytes $text.TrimEnd()) }
        $url = Get-QPAssetUrl 'v0.5.0' 'install.ps1'
        Assert-QPDownloadUrl $url $url
        Assert-QPDownloadUrl 'https://release-assets.githubusercontent.com/x?token=y' $url
        foreach ($bad in @('http://github.com/x', 'https://github.com.evil.test/x', 'https://127.0.0.1/x', 'https://u:p@objects.githubusercontent.com/x', 'https://objects.githubusercontent.com:444/x', 'https://github.com/other/repo', 'https://objects.githubusercontent.com/x#frag', 'https://github.com\@evil.test/x')) { Refuses { Assert-QPDownloadUrl $bad $url } }
        Refuses { Assert-QPDownloadUrl 'https://api.github.com/repos/LinyuFiee/QQON-Panel/releases/latest?x=1' 'https://api.github.com/repos/LinyuFiee/QQON-Panel/releases/latest' -Api }
    }

    Test 'listener/origin/build-info validation and fixed Windows command' {
        $config = New-TestConfig
        $paths = Get-QPPaths
        Check ($paths.Code -ceq ([IO.Path]::Combine([Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFiles), 'QQON-Panel')))
        Check ($paths.Data -ceq ([IO.Path]::Combine([Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData), 'QQON-Panel\data')))
        Check ((Get-QPOrigin 5555 'https://Panel.Example.com:443/').Host -ceq 'panel.example.com')
        Check ((Get-QPOrigin 5555 '').Host -ceq '127.0.0.1:5555')
        foreach ($origin in @('http://panel.example.com', 'https://127.0.0.1', 'https://[::1]', 'https://example.com/x', 'https://u@example.com', 'https://example.com?x=1', 'https://example.com:0443', 'https://-x.example', 'https://example.com#x')) { Refuses { Get-QPOrigin 5173 $origin } }
        Refuses { Get-QPOrigin 80 '' }; Refuses { Get-QPOrigin 65536 '' }
        $build = @{ version = $config['version']; commit = $config['commit']; buildTime = $config['buildTime']; repository = $config['repository']; dataSchema = 1; os = 'windows'; arch = 'amd64' }
        Assert-QPBuildInfo (ConvertTo-QPJsonBytes $build) $config
        $build['version'] = '0.5.1'; Refuses { Assert-QPBuildInfo (ConvertTo-QPJsonBytes $build) $config } 'differs'
        $command = Get-QPServiceCommand $paths $config
        Check ($command.Contains(' -listen "127.0.0.1:5173" -data-dir "') -and $command.EndsWith('-github-repo LinyuFiee/QQON-Panel') -and -not $command.Contains('-service'))
        Check ((Get-QPServiceSid).Value -ceq 'S-1-5-80-3082832221-4160798672-3351094347-672270584-4179449247')
    }

    Test 'private ACL descriptors protect future code/data and admin metadata' {
        foreach ($kind in @('Code', 'State', 'Data', 'Admin')) {
            $acl = New-QPSecurity $kind
            Check $acl.AreAccessRulesProtected
            Check ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ceq 'S-1-5-32-544')
            $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
            $service = @($rules | Where-Object { $_.IdentityReference.Value -ceq (Get-QPServiceSid).Value })
            if ($kind -eq 'Admin') { Check ($service.Count -eq 0) }
            else {
                Check ($service.Count -eq 1)
                $expected = (Get-QPRights $kind)[(Get-QPServiceSid).Value]
                Check (([long]$service[0].FileSystemRights -band $expected) -eq $expected)
                if ($kind -eq 'State') { Check ($service[0].InheritanceFlags -eq [Security.AccessControl.InheritanceFlags]::None) }
                else { Check ($service[0].InheritanceFlags -eq [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit') }
            }
        }
        Refuses { Assert-QPAcl $script:Root 'Code' -Root } 'owner|ACL|access'
    }

    Test 'native x64 PE fixture and bounded build-info subprocess' {
        $script:FixtureExe = [IO.Path]::Combine($script:Root, 'fixture.exe')
        $parameters = [CodeDom.Compiler.CompilerParameters]::new()
        $parameters.CompilerOptions = '/platform:x64'; $parameters.GenerateExecutable = $true; $parameters.OutputAssembly = $script:FixtureExe
        $provider = [Microsoft.CSharp.CSharpCodeProvider]::new()
        $source = @'
using System;
using System.Threading;
public static class TestPanel {
  public static int Main(string[] args) {
    if (args.Length == 1 && args[0] == "-build-info") {
      Console.WriteLine("{\"version\":\"0.6.0\",\"commit\":\"cccccccccccccccccccccccccccccccccccccccc\",\"buildTime\":\"2026-11-07T00:00:00Z\",\"repository\":\"LinyuFiee/QQON-Panel\",\"dataSchema\":1,\"os\":\"windows\",\"arch\":\"amd64\"}"); return 0;
    }
    if (args.Length == 1 && args[0] == "timeout") { Thread.Sleep(20000); return 0; }
    if (args.Length == 1 && args[0] == "large") { Console.Write(new string('x',100000)); return 0; }
    return 9;
  }
}
'@
        try { $compiled = $provider.CompileAssemblyFromSource($parameters, $source); Check (-not $compiled.Errors.HasErrors) ($compiled.Errors | Out-String) } finally { $provider.Dispose() }
        Assert-QPPE $script:FixtureExe
        $raw = Invoke-QPProcess $script:FixtureExe @('-build-info') 10000
        Assert-QPBuildInfo $raw (New-TestConfig)
        Refuses { Invoke-QPProcess $script:FixtureExe @('timeout') 250 } 'deadline'
        Refuses { Invoke-QPProcess $script:FixtureExe @('large') 5000 1024 } 'output'
        Refuses { Invoke-QPProcess $script:FixtureExe @('bad') 5000 } 'exit code'
        Check (@(Get-QPBinaryProcesses $script:FixtureExe).Count -eq 0)
        $script:Payload = [IO.File]::ReadAllBytes($script:FixtureExe)
    }

    Test 'bounded ZIP only regular panel.exe, hash then PE before execution' {
        Check ($null -ne $script:Payload) 'PE fixture missing'
        function New-QPSecurity { param($Kind, [switch]$File) return New-TestSecurity -File:$File }
        $zip = New-TestZip $script:Payload
        $path = [IO.Path]::Combine($script:Root, 'extracted.exe')
        Assert-TestPath $path
        Expand-QPWindowsZip $zip (Expected-Zip $zip $script:Payload) $path
        Check ((Get-QPFileHash $path) -ceq (Get-QPSha256 $script:Payload))
        $central = [int][BitConverter]::ToUInt32($zip, $zip.Length - 6)
        foreach ($mutation in @(
            { param($z,$c) $z[$c+8] = 1 }, { param($z,$c) $z[$c+8] = 8 },
            { param($z,$c) $z[$c+28] = 8 }, { param($z,$c) $z[$c+30] = 1 },
            { param($z,$c) $z[$c+41] = 0xa1 }, { param($z,$c) $z[$c+38] = 16 },
            { param($z,$c) $z[30] = 47 }, { param($z,$c) $z[$z.Length-12] = 2 },
            { param($z,$c) $z[28] = 1 }, { param($z,$c) $z[$c+46] = 80 },
            { param($z,$c) $z[14] = $z[14] -bxor 1; $z[$c+16] = $z[$c+16] -bxor 1 }
        )) {
            $bad = [byte[]]$zip.Clone(); & $mutation $bad $central
            $destination = [IO.Path]::Combine($script:Root, [guid]::NewGuid().ToString('N') + '.exe')
            Refuses { Expand-QPWindowsZip $bad (Expected-Zip $bad $script:Payload) $destination }
        }
        $bad = [byte[]]($zip + [byte]0); Refuses { Expand-QPWindowsZip $bad (Expected-Zip $bad $script:Payload) ([IO.Path]::Combine($script:Root,'trailer.exe')) }
        $expected = Expected-Zip $zip $script:Payload; $expected['sha256'] = 'a'*64
        Refuses { Expand-QPWindowsZip $zip $expected ([IO.Path]::Combine($script:Root,'inner-bad.exe')) } 'SHA-256'
        $badPe = [byte[]]$script:Payload.Clone(); $offset = [BitConverter]::ToInt32($badPe, 60); $badPe[$offset+4] = 0x4c; $badPe[$offset+5] = 0x01
        $path32 = [IO.Path]::Combine($script:Root, 'x86.exe'); [IO.File]::WriteAllBytes($path32,$badPe)
        Refuses { Assert-QPPE $path32 } 'AMD64'
    }

    Test 'exclusive file unlock probe and atomic replace are real temporary I/O' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        $paths = New-TestPaths
        [IO.File]::WriteAllBytes($paths.Binary, (Bytes 'old')); [IO.File]::WriteAllBytes($paths.Candidate, (Bytes 'new'))
        $held = [IO.FileStream]::new($paths.Binary,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None)
        try { Check (-not (Test-QPFileUnlocked $paths.Binary)) } finally { $held.Dispose() }
        Check (Test-QPFileUnlocked $paths.Binary)
        Switch-QPBinary $paths
        Check ([IO.File]::ReadAllText($paths.Binary) -ceq 'new' -and [IO.File]::ReadAllText($paths.Previous) -ceq 'old' -and -not [IO.File]::Exists($paths.Candidate))
    }

    Test 'update journals stop-before-switch, previous backup and health-before-commit' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Stop-QPServiceAndWait { param($Paths,$Config) Check ([IO.File]::Exists($Paths.Journal)); [void]$events.Add('stop'); Check ([IO.File]::ReadAllText($Paths.Binary) -ceq 'old') }
        function Start-QPService { param($Paths,$Config) Check ([IO.File]::ReadAllText($Paths.Binary) -ceq 'new'); [void]$events.Add('start') }
        function Wait-QPHealthy { param($Paths,$Config) Check ([IO.File]::ReadAllText($Paths.Previous) -ceq 'old'); Check ((ConvertFrom-QPJson (Read-QPFile $Paths.Config 32768))['version'] -ceq '0.5.0'); [void]$events.Add('health') }
        $paths = New-TestPaths; $events = [Collections.Generic.List[string]]::new()
        $old = New-TestConfig '0.5.0' (Bytes 'old'); $new = New-TestConfig '0.5.1' (Bytes 'new')
        [IO.File]::WriteAllBytes($paths.Binary,(Bytes 'old')); [IO.File]::WriteAllBytes($paths.Candidate,(Bytes 'new'))
        [IO.File]::WriteAllBytes($paths.Config,(ConvertTo-QPJsonBytes $old))
        $sentinel = [IO.Path]::Combine($paths.Data,'sentinel'); [IO.File]::WriteAllText($sentinel,'preserve')
        Invoke-QPUpdateTransaction $paths $old $new
        Check (($events -join ',') -ceq 'stop,start,health')
        Check ((ConvertFrom-QPJson (Read-QPFile $paths.Config 32768))['version'] -ceq '0.5.1')
        Check (-not [IO.File]::Exists($paths.Journal) -and [IO.File]::ReadAllText($sentinel) -ceq 'preserve')
    }

    Test 'failed health restores previous atomically, rechecks old health and never resets data' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Assert-QPBinary { param($Path,$Config,[switch]$ReadOnly) Assert-TestPath $Path; Check ((Get-QPFileHash $Path) -ceq $Config['binarySHA256']) }
        function Stop-QPServiceAndWait { param($Paths,$Config) [void]$events.Add('stop') }
        function Start-QPService { param($Paths,$Config) [void]$events.Add('start-'+$Config['version']) }
        function Wait-QPHealthy { param($Paths,$Config) [void]$events.Add('health-'+$Config['version']); if ($Config['version'] -ceq '0.5.1') { throw 'injected health failure' } }
        $paths = New-TestPaths; $events = [Collections.Generic.List[string]]::new()
        $old = New-TestConfig '0.5.0' (Bytes 'old'); $new = New-TestConfig '0.5.1' (Bytes 'new')
        [IO.File]::WriteAllBytes($paths.Binary,(Bytes 'old')); [IO.File]::WriteAllBytes($paths.Candidate,(Bytes 'new')); [IO.File]::WriteAllBytes($paths.Config,(ConvertTo-QPJsonBytes $old))
        $sentinel = [IO.Path]::Combine($paths.Data,'sentinel'); [IO.File]::WriteAllText($sentinel,'keep')
        Refuses { Invoke-QPUpdateTransaction $paths $old $new } 'previous version was atomically restored'
        Check (($events -join ',') -ceq 'stop,start-0.5.1,health-0.5.1,stop,start-0.5.0,health-0.5.0')
        Check ([IO.File]::ReadAllText($paths.Binary) -ceq 'old' -and -not [IO.File]::Exists($paths.Journal) -and [IO.File]::ReadAllText($sentinel) -ceq 'keep')
    }

    Test 'rollback failure retains journal; stop timeout never switches executable' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Stop-QPServiceAndWait { param($Paths,$Config) throw 'stop timeout' }
        function Switch-QPBinary { throw 'UNEXPECTED SWITCH' }
        $paths = New-TestPaths; $old = New-TestConfig '0.5.0' (Bytes 'old'); $new = New-TestConfig '0.5.1' (Bytes 'new')
        [IO.File]::WriteAllBytes($paths.Binary,(Bytes 'old')); [IO.File]::WriteAllBytes($paths.Candidate,(Bytes 'new')); [IO.File]::WriteAllBytes($paths.Config,(ConvertTo-QPJsonBytes $old))
        Refuses { Invoke-QPUpdateTransaction $paths $old $new } 'rollback is incomplete'
        Check ([IO.File]::Exists($paths.Journal) -and [IO.File]::ReadAllText($paths.Binary) -ceq 'old' -and -not [IO.File]::Exists($paths.Previous))
        $journal = Read-QPTransaction $paths; Check ($journal['phase'] -ceq 'prepared')
        $journal['new']['listen'] = '127.0.0.1:5555'; $journal['new']['healthHost'] = '127.0.0.1:5555'
        [IO.File]::WriteAllBytes($paths.Journal,(ConvertTo-QPJsonBytes $journal))
        Refuses { Read-QPTransaction $paths } 'fixed'
    }

    Test 'read-only status cannot lock, recover, download or mutate' {
        function Assert-QPNativeHost { param([switch]$Administrator) Check (-not $Administrator) }
        function Get-QPPaths { return $paths }
        function Get-QPService { return $null }
        function Enter-QPLock { throw 'MUTATION mutex' }
        function Resume-QPTransaction { throw 'MUTATION recovery' }
        function Get-QPReleaseMetadata { throw 'NETWORK release' }
        function Write-QPAtomicJson { throw 'MUTATION write' }
        function New-QPPrivateDirectory { throw 'MUTATION mkdir' }
        function Invoke-QPProcess { throw 'EXECUTE binary' }
        $base = [IO.Path]::Combine($script:Root,'missing-status')
        $paths = @{ Code = $base+'\code'; State = $base+'\state' }
        Invoke-QPInstaller @{ Action='Status'; PortSpecified=$false; OriginSpecified=$false; Version='v0.5.0' } 'unused'
        Check (-not [IO.Directory]::Exists($base))
        function Assert-QPLayout { param($Paths) }
        function Read-QPTransaction { param($Paths) return @{kind='update';phase='ready'} }
        $paths = New-TestPaths; [IO.File]::WriteAllText($paths.Journal,'unchanged')
        Show-QPStatus $paths
        Check ([IO.File]::ReadAllText($paths.Journal) -ceq 'unchanged')
    }

    Test 'non-admin preflight/negative options refuse before changes and menu Exit is harmless' {
        function Assert-QPNativeHost { param([switch]$Administrator) if ($Administrator) { throw 'Administrator required' } }
        function Enter-QPLock { throw 'UNEXPECTED LOCK' }
        Refuses { Invoke-QPInstaller @{Action='Install';Yes=$true;Port=5173;Origin='';Version='';PortSpecified=$false;OriginSpecified=$false} 'unused' } 'Administrator'
        function Read-QPConsole { param($Prompt) return '0' }
        Invoke-QPInstaller @{Action='';Yes=$false;Port=5173;Origin='';Version='';PortSpecified=$false;OriginSpecified=$false} 'unused'
        Refuses { Assert-QPPlainPath '\\server\share\x' -AllowMissing }
        Refuses { Assert-QPPlainPath 'C:\x\..\y' -AllowMissing }
        Refuses { Assert-QPPlainPath 'C:\x:stream' -AllowMissing }
    }

    Test 'first installation uses private files, actual PE/build-info and durable journal before SCM' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function Assert-QPInstallParent { param($Path) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Get-QPService { return $null }
        function Get-QPReleaseMetadata { param($Version) return $metadata }
        function Read-QPVerifiedAsset { param($Metadata,$Name) return ,$assets[$Name] }
        function Register-QPService { param($Paths,$Config) Check ([IO.File]::Exists($Paths.Journal)); Check ([IO.File]::Exists($Paths.Binary)); [void]$events.Add('register') }
        function Start-QPService { param($Paths,$Config) $j=Read-QPTransaction $Paths; Check ($j['phase'] -ceq 'ready'); [void]$events.Add('start') }
        function Wait-QPHealthy { param($Paths,$Config) Check (-not [IO.File]::Exists($Paths.Config)); [void]$events.Add('health') }
        $paths = New-TestPaths
        [IO.Directory]::Delete($paths.Data); [IO.Directory]::Delete($paths.State); [IO.Directory]::Delete($paths.Code)
        $metadata = New-Fixture; $zip = New-TestZip $script:Payload
        $assets = @{ 'install.ps1' = [IO.File]::ReadAllBytes([IO.Path]::Combine($script:Scripts,'install.ps1')); 'update.ps1' = [IO.File]::ReadAllBytes([IO.Path]::Combine($script:Scripts,'update.ps1')); 'panel-windows-amd64.zip' = $zip }
        foreach ($name in $assets.Keys) { $metadata.Sums[$name] = Get-QPSha256 $assets[$name]; $metadata.Release.Sizes[$name] = $assets[$name].Length }
        $record = Get-QPWindowsRecord $metadata; $record['size'] = $script:Payload.Length; $record['sha256'] = Get-QPSha256 $script:Payload; $record['archive']['size'] = $zip.Length; $record['archive']['sha256'] = Get-QPSha256 $zip
        $events = [Collections.Generic.List[string]]::new()
        Install-QPPanel $paths 5555 '' 'v0.5.0' ([IO.Path]::Combine($script:Scripts,'install.ps1'))
        Check (($events -join ',') -ceq 'register,start,health')
        $config = Read-QPConfig $paths
        Check ($config['listen'] -ceq '127.0.0.1:5555' -and -not [IO.File]::Exists($paths.Journal))
        Check ([IO.Directory]::GetAccessControl($paths.Data).AreAccessRulesProtected)
        Check ([IO.File]::GetAccessControl($paths.Config).AreAccessRulesProtected)
        Check (@([IO.Directory]::EnumerateFileSystemEntries($paths.Data)).Count -eq 0)
    }

    Test 'prepared and ready interrupted updates recover conservatively with preserved data' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Assert-QPLayout { param($Paths) Assert-TestPath $Paths.Code; Assert-TestPath $Paths.Data }
        function Assert-QPManagers { param($Paths,$Config) Assert-TestPath $Paths.Installer }
        function Assert-QPBinary { param($Path,$Config,[switch]$ReadOnly) Check ((Get-QPFileHash $Path) -ceq $Config['binarySHA256']) }
        function Assert-QPService { param($Paths,$Config,$Service) }
        function Get-QPService { return @{Name='QQONPanel'} }
        function Stop-QPServiceAndWait { param($Paths,$Config) }
        function Start-QPService { param($Paths,$Config) Check ($Config['version'] -ceq '0.5.0') }
        function Wait-QPHealthy { param($Paths,$Config) Check ([IO.File]::ReadAllText($Paths.Binary) -ceq 'old') }
        foreach ($phase in @('prepared','ready')) {
            $paths = New-TestPaths; $old=New-TestConfig '0.5.0' (Bytes 'old'); $new=New-TestConfig '0.5.1' (Bytes 'new')
            [IO.File]::WriteAllBytes($paths.Config,(ConvertTo-QPJsonBytes $old))
            [IO.File]::WriteAllBytes($paths.Binary,(Bytes 'old'))
            if ($phase -ceq 'ready') { [IO.File]::WriteAllBytes($paths.Previous,(Bytes 'old')); [IO.File]::WriteAllBytes($paths.Binary,(Bytes 'new')) }
            else { [IO.File]::WriteAllBytes($paths.Candidate,(Bytes 'new')) }
            [IO.File]::WriteAllBytes($paths.Journal,(ConvertTo-QPJsonBytes @{format=$new['format'];kind='update';phase=$phase;old=$old;new=$new}))
            $sentinel=[IO.Path]::Combine($paths.Data,'keep'); [IO.File]::WriteAllText($sentinel,'kept')
            Resume-QPTransaction $paths
            Check (-not [IO.File]::Exists($paths.Journal) -and [IO.File]::ReadAllText($paths.Binary) -ceq 'old' -and [IO.File]::ReadAllText($sentinel) -ceq 'kept')
        }
    }

    Test 'SCM adapter requests actual own-process virtual account and SID configuration' {
        function Get-QPService { return $null }
        function Invoke-QPProcess { param($File,$Arguments,$Timeout,$Limit) [void]$calls.Add(@($Arguments)); if ($Arguments[0] -ceq 'sidtype') { throw 'MOCK END BEFORE ACCOUNT LOOKUP' }; return ,(Bytes 'ok') }
        $calls = [Collections.Generic.List[object]]::new(); $paths = New-TestPaths; $config = New-TestConfig
        Refuses { Register-QPService $paths $config } 'MOCK END'
        Check ($calls.Count -eq 2 -and $calls[0][0] -ceq 'create' -and $calls[0][1] -ceq 'QQONPanel')
        Check ($calls[0] -ccontains 'NT SERVICE\QQONPanel' -and $calls[0] -ccontains 'own' -and $calls[0] -ccontains 'auto')
        Check ($calls[0][3] -ceq (Get-QPServiceCommand $paths $config))
        Check (($calls[1] -join ',') -ceq 'sidtype,QQONPanel,unrestricted')
    }

    Test 'stop adapter waits through process and file-unlock gates using mock SCM' {
        Add-Type -AssemblyName System.ServiceProcess
        function Get-QPService { return @{ProcessId=0} }
        function Assert-QPService { param($Paths,$Config,$Service) }
        function New-QPServiceController { return $controller }
        function Get-QPBinaryProcesses { param($Binary) [void]$events.Add('process'); $counters.Process++; if ($counters.Process -eq 1) { return 123 } }
        function Test-QPFileUnlocked { param($Path) [void]$events.Add('unlock'); $counters.Unlock++; return ($counters.Unlock -ge 2) }
        function Start-Sleep { param($Milliseconds) }
        $events = [Collections.Generic.List[string]]::new(); $counters=@{Process=0;Unlock=0}
        $controller = [pscustomobject]@{Status=[ServiceProcess.ServiceControllerStatus]::Running;Stopped=$false;Disposed=$false}
        $controller | Add-Member ScriptMethod Refresh { }
        $controller | Add-Member ScriptMethod Stop { $this.Stopped=$true; $this.Status=[ServiceProcess.ServiceControllerStatus]::Stopped }
        $controller | Add-Member ScriptMethod Dispose { $this.Disposed=$true }
        Stop-QPServiceAndWait (New-TestPaths) (New-TestConfig)
        Check ($controller.Stopped -and $controller.Disposed -and $counters.Process -eq 3 -and $counters.Unlock -eq 2)
        Check (($events -join ',') -ceq 'process,process,unlock,process,unlock')
    }

    Test 'wrapper validates flat config keys and rejects invalid versions before paths' {
        $config = New-TestConfig
        $text = [Text.UTF8Encoding]::new($false).GetString((ConvertTo-QPJsonBytes $config))
        $keys=[regex]::Matches($text, '"((?:[^"\\]|\\.)*)"\s*:')
        Check ($keys.Count -eq 18)
        Check ((Read-QPWrapperIdentity (ConvertTo-QPJsonBytes $config)).privilegeMode -ceq 'restricted')
        foreach ($bad in @('0.5.0','v0.5.0; whoami','v0.5.0-beta','v01.5.0')) { Refuses { Invoke-QPInstalledUpdater $bad $true } 'Version' }
        $temporary = [IO.Path]::Combine($script:Root,'wrapper-owner'); [void][IO.Directory]::CreateDirectory($temporary)
        Refuses { Assert-QPWrapperPath $temporary } 'owner|protected'
    }

    Test 'all test host policy remained unchanged' { Check ((Get-ExecutionPolicy -List | Out-String) -ceq $script:PolicyBefore) }
} finally {
    # This directory was created by this test, never populated from a downloaded archive path.
    # The extractor always used caller-chosen individual file names, no recursive archive extraction.
    if ([IO.Directory]::Exists($script:Root)) { [IO.Directory]::Delete($script:Root, $true) }
}
[Console]::WriteLine(('Windows tests: {0} passed, {1} failed; temporary/mock only, no host service or installation mutations.' -f $script:Passed, $script:Failed))
if ($script:Failed -gt 0) { exit 1 }
