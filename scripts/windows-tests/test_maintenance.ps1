# v0.6 原生 PowerShell 隔离维护测试。所有写入在本次新建 TEMP；SCM/PATH/账户均为 fake。
[CmdletBinding()]
param([string]$PanelBinary = '')
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
$script:Passed = 0; $script:Failed = 0
$script:Scripts = [IO.Path]::GetFullPath([IO.Path]::Combine($PSScriptRoot, '..'))
$script:Root = [IO.Path]::Combine([IO.Path]::GetTempPath(), 'QQON-maintenance-tests-' + [guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($script:Root)
$script:PolicyBefore = (Get-ExecutionPolicy -List | Out-String)
$script:ProcessPathBefore = $env:PATH
$script:CallbackBefore = [Net.ServicePointManager]::ServerCertificateValidationCallback

function Check { param([bool]$Condition, [string]$Message = '断言失败'); if (-not $Condition) { throw $Message } }
function Refuses {
    param([scriptblock]$Body, [string]$Pattern = '.')
    $caught = $false
    try { & $Body | Out-Null } catch { $caught = $true; Check ($_.Exception.Message -match $Pattern) ('拒绝原因不符：' + $_.Exception.Message) }
    Check $caught '应拒绝但实际成功。'
}
function Test {
    param([string]$Name, [scriptblock]$Body)
    try { & $Body; $script:Passed++; [Console]::WriteLine('PASS ' + $Name) }
    catch { $script:Failed++; [Console]::Error.WriteLine('FAIL ' + $Name + '：' + $_.Exception.Message + "`n" + $_.ScriptStackTrace) }
}
function Bytes { param([string]$Value); return ,([Text.UTF8Encoding]::new($false).GetBytes($Value)) }
function Assert-TestPath {
    param([string]$Path)
    Check ([IO.Path]::GetFullPath($Path).StartsWith($script:Root + '\', [StringComparison]::OrdinalIgnoreCase)) ('禁止访问非临时目标：' + $Path)
    Assert-QPPlainPath $Path -AllowMissing
}
function New-TestSecurity {
    param([switch]$File)
    if ($File) { $acl = [Security.AccessControl.FileSecurity]::new() } else { $acl = [Security.AccessControl.DirectorySecurity]::new() }
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl.SetOwner($sid); $acl.SetAccessRuleProtection($true, $false)
    $inherit = [Security.AccessControl.InheritanceFlags]::None
    if (-not $File) { $inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit' }
    foreach ($id in @($sid, [Security.Principal.SecurityIdentifier]::new('S-1-5-18'), [Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) { $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($id, [Security.AccessControl.FileSystemRights]::FullControl, $inherit, [Security.AccessControl.PropagationFlags]::None, [Security.AccessControl.AccessControlType]::Allow)) }
    return $acl
}
function New-TestPaths {
    $base = [IO.Path]::Combine($script:Root, [guid]::NewGuid().ToString('N')); [void][IO.Directory]::CreateDirectory($base)
    $code = [IO.Path]::Combine($base, 'QQON-Panel'); $state = [IO.Path]::Combine($base, 'state'); $data = [IO.Path]::Combine($state, 'data')
    foreach ($path in @($code, $state, $data)) { [IO.DirectoryInfo]::new($path).Create((New-TestSecurity)) }
    return @{ Code = $code; State = $state; Data = $data; Binary = "$code\panel.exe"; Candidate = "$code\panel.next.exe"; Previous = "$code\panel.previous.exe"; Installer = "$code\install.ps1"; Updater = "$code\update.ps1"; Config = "$state\install.json"; Journal = "$state\transaction.json" }
}
function New-TestConfig {
    $value = @{ format = 2; repository = 'LinyuFiee/QQON-Panel'; service = 'QQONPanel'; listen = '127.0.0.1:8443'; origin = ''; healthHost = '127.0.0.1:8443'; version = '0.6.0'; dataSchema = 1; commit = 'c' * 40; buildTime = '2026-11-07T00:00:00Z'; binarySize = 3; binarySHA256 = Get-QPSha256 (Bytes 'app'); installerSHA256 = 'a' * 64; updaterSHA256 = 'b' * 64; accessMode = 'local'; tls = $null }
    return Read-QPConfigValue $value
}
function New-TestTls {
    param([string]$Generation = ('1' * 32))
    return @{ generation = $Generation; host = '8.8.8.8'; fingerprint = 'e' * 64; notAfter = [datetime]::UtcNow.AddDays(365).ToString("yyyy-MM-dd'T'HH:mm:ss'Z'") }
}
function Save-TestInstall {
    param([hashtable]$Paths, [hashtable]$Config)
    [IO.File]::WriteAllBytes($Paths.Binary, (Bytes 'app'))
    foreach ($name in @('install.ps1', 'update.ps1')) { [IO.File]::Copy([IO.Path]::Combine($script:Scripts, $name), [IO.Path]::Combine($Paths.Code, $name)) }
    $Config['installerSHA256'] = Get-QPFileHash $Paths.Installer 524288; $Config['updaterSHA256'] = Get-QPFileHash $Paths.Updater 32768
    Write-QPAtomicJson $Paths.Config $Config
}
function Capture-Console {
    param([scriptblock]$Body)
    $writer = [IO.StringWriter]::new(); $original = [Console]::Out
    try { [Console]::SetOut($writer); & $Body | Out-Null; return $writer.ToString() }
    finally { [Console]::SetOut($original); $writer.Dispose() }
}
function Read-LegacyAsset {
    param([string]$Name)
    # Read the local official tag as bytes; never run downloaded code or git hooks.
    $repo = [IO.Path]::GetFullPath([IO.Path]::Combine($script:Scripts, '..'))
    $git = (Get-Command git.exe -ErrorAction Stop).Source
    return ,(Invoke-QPProcess $git @('-C', $repo, 'show', ('v0.5.0:scripts/' + $Name)) 15000 524288)
}
function New-LegacyConfig {
    $old = New-TestConfig
    $old['format'] = 1; $old.Remove('accessMode'); $old.Remove('tls'); $old['version'] = '0.5.0'
    $old['installerSHA256'] = 'db0bf026d4debb409fd14b30f0837cc7ba4d5a3f48cfec121545feff8c3931c8'
    $old['updaterSHA256'] = '383e0eec3c969ed9dd93ee8e05c1ebc4fd2d12bb4162dda6300025cfe951278d'
    return Read-QPConfigValue $old -AllowLegacy
}

function New-MigrationCrashFixture {
    $paths = New-TestPaths; $old = New-LegacyConfig
    $assets = @{ 'install.ps1' = [IO.File]::ReadAllBytes([IO.Path]::Combine($script:Scripts, 'install.ps1')); 'update.ps1' = [IO.File]::ReadAllBytes([IO.Path]::Combine($script:Scripts, 'update.ps1')) }
    [IO.File]::WriteAllBytes($paths.Installer, $script:LegacyInstaller); [IO.File]::WriteAllBytes($paths.Updater, $script:LegacyUpdater)
    [IO.File]::WriteAllBytes($paths.Binary, (Bytes 'app')); Write-QPAtomicJson $paths.Config $old
    [IO.File]::WriteAllText([IO.Path]::Combine($paths.Data, 'account.json'), 'CRASH_ACCOUNT_SENTINEL')
    $new = $old.Clone(); $new['format'] = 2; $new['accessMode'] = 'local'; $new['tls'] = $null
    $new['installerSHA256'] = Get-QPSha256 $assets['install.ps1']; $new['updaterSHA256'] = Get-QPSha256 $assets['update.ps1']
    $transaction = @{ format = 2; kind = 'migrate'; phase = 'prepared'; old = $old; new = $new; wasRunning = $true; backup = [guid]::NewGuid().ToString('N') }
    Write-QPAtomicJson $paths.Journal $transaction
    [void](New-QPBackupSnapshot $paths $old $transaction['backup'])
    return @{ Paths = $paths; Old = $old; New = $new; Transaction = $transaction; Assets = $assets }
}

try {
    . ([IO.Path]::Combine($script:Scripts, 'install.ps1'))
    . ([IO.Path]::Combine($script:Scripts, 'update.ps1'))
    # Hard fail if a test leaks into host service/process/network discovery or mutating PATH.
    function Get-CimInstance { throw '禁止真实 CIM/SCM' }
    function New-QPServiceController { throw '禁止真实 ServiceController' }
    function Add-QPSystemPath { throw '禁止真实系统 PATH 写入' }
    function Enter-QPLock { throw '禁止真实全局互斥锁' }
    function Get-QPReleaseMetadata { throw '禁止真实公网元数据请求' }
    function Read-QPDownload { throw '禁止真实公网下载' }

    Test 'format2 精确字段及公共 IPv4 默认配置' {
        $access = Get-QPAccess public-selfsigned 8443 8.8.8.8
        $config = Set-QPAccessValue (New-TestConfig) $access (New-TestTls)
        Check ($config.Count -eq 16 -and $config['tls'].Count -eq 4 -and $config['dataSchema'] -eq 1)
        Check ($config['listen'] -ceq '0.0.0.0:8443' -and (Get-QPWebUrl $config) -ceq 'https://8.8.8.8:8443/')
        $paths = New-TestPaths; $command = Get-QPServiceCommand $paths $config
        $tls = Get-QPTlsPaths $paths ('1' * 32)
        Check ($command.Contains(' -tls-cert "' + $tls.Certificate + '" -tls-key "' + $tls.Key + '"'))
        Check (-not $command.Contains('powershell') -and $command.EndsWith('-github-repo LinyuFiee/QQON-Panel'))
        foreach ($name in @('127.0.0.1','10.0.0.1','172.16.0.1','192.168.1.1','169.254.1.1','100.64.0.1','0.0.0.0','224.0.0.1','240.0.0.1','198.18.0.1','192.0.2.1','198.51.100.2','203.0.113.1','127.1','08.8.8.8','::1','panel.example.com')) { Refuses { Get-QPAccess public-selfsigned 8443 $name } }
        Refuses { Get-QPAccess public-selfsigned 443 8.8.8.8 }
        $bad = $config.Clone(); $bad['tls'] = $config['tls'].Clone(); $bad['tls']['path'] = 'C:\arbitrary.pem'; Refuses { Read-QPConfigValue $bad } 'QP_JSON'
        $bad = $config.Clone(); $bad['healthHost'] = 'other.example:8443'; Refuses { Read-QPConfigValue $bad } '不一致'
        $bad = $config.Clone(); $bad['tls'] = $config['tls'].Clone(); $bad['tls']['generation'] = '..\escape'; Refuses { Read-QPConfigValue $bad } '代次'
        $bad = $config.Clone(); $bad['dataSchema'] = 2; Refuses { Read-QPConfigValue $bad } 'QP_IDENTITY'
        Check ((Get-QPAccess public-certificate 9443 'Panel.Example.com').host -ceq 'panel.example.com')
        Check ((Read-QPWrapperIdentity (ConvertTo-QPJsonBytes $config)).format -eq 2)
        $duplicate = ([Text.Encoding]::UTF8.GetString((ConvertTo-QPJsonBytes $config))).Replace('"format":2', '"format":2,"Format":2')
        Refuses { Read-QPWrapperIdentity (Bytes $duplicate) } '重复'
    }

    Test 'TLS ACL 描述符 service只读且root/files受保护' {
        foreach ($file in @($false, $true)) {
            $acl = New-QPSecurity Tls -File:$file; Check $acl.AreAccessRulesProtected
            Check ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ceq 'S-1-5-32-544')
            $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
            $service = @($rules | Where-Object { $_.IdentityReference.Value -ceq (Get-QPServiceSid).Value })
            Check ($service.Count -eq 1)
            $write = [long][Security.AccessControl.FileSystemRights]'Write, Delete, ChangePermissions, TakeOwnership'
            Check (([long]$service[0].FileSystemRights -band $write) -eq 0)
            if (-not $file) { Check ($service[0].InheritanceFlags -eq [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit') }
        }
    }

    Test 'CLI白名单/无参menu/tls renew两词/拒shell参数' {
        foreach ($words in @(@(), @('status'), @('url'), @('start','--yes'), @('stop'), @('restart'), @('logs','--lines','200'), @('update','--version','v0.6.1','--yes'), @('backup'), @('config'), @('tls','renew','--yes'), @('help'))) {
            $a = Read-QPMaintenanceCommand $words; $b = Read-QPWrapperCommand $words
            foreach ($field in @('Action','Yes','Version','Lines')) { Check ($a[$field] -ceq $b[$field]) }
        }
        foreach ($words in @(@('tls'), @('tls','renew','anything'), @('logs','--lines','201'), @('logs','--lines','0'), @('status','--yes'), @('update','--version','v01.6.0'), @('update','--yes','--yes'), @('start;whoami'), @('logs','C:\secret'), @('config','&'))) {
            Refuses { Read-QPMaintenanceCommand $words }; Refuses { Read-QPWrapperCommand $words }
        }
        function Read-QPConsole { param($Prompt) return '0' }
        Invoke-QPMaintenance @()
    }

    Test '生成qqon.ps1/cmd BOM模板/本机摘要保护/无新增assets' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        $paths = New-TestPaths; $config = New-TestConfig; Save-TestInstall $paths $config
        Write-QPShortcuts $paths; Assert-QPShortcuts $paths
        $path = [IO.Path]::Combine($paths.Code, 'qqon.ps1'); $raw = [IO.File]::ReadAllBytes($path)
        Check ($raw[0] -eq 0xef -and $raw[1] -eq 0xbb -and $raw[2] -eq 0xbf)
        $tokens = $null; $errors = $null; [void][Management.Automation.Language.Parser]::ParseFile($path,[ref]$tokens,[ref]$errors); Check ($errors.Count -eq 0) ($errors | Out-String)
        . $path
        Check ((Read-QPWrapperCommand @('tls','renew')).Action -ceq 'tls')
        Check ([IO.File]::ReadAllText([IO.Path]::Combine($paths.Code,'qqon.cmd')).Contains('"%~dp0qqon.ps1" %*'))
        Check ((Get-QPAssetLimits).Count -eq 9)
        [IO.File]::AppendAllText($path, 'tamper')
        Refuses { Assert-QPShortcuts $paths } '变化'
        [IO.File]::AppendAllText($paths.Installer, 'tamper')
        Refuses { Assert-QPManagers $paths $config } 'QP_INTEGRITY'
    }

    Test 'PATH仅纯函数append保留现有字节，拒任意目录' {
        $paths = New-TestPaths; $before = '%SystemRoot%\system32;C:\existing;;'
        $next = Get-QPPathAppend $before $paths.Code
        Check ($next.Changed -and $next.Value -ceq ($before + $paths.Code))
        $again = Get-QPPathAppend $next.Value $paths.Code; Check (-not $again.Changed -and $again.Value -ceq $next.Value)
        Refuses { Get-QPPathAppend $before 'C:\arbitrary' } '固定'
        Refuses { Get-QPPathAppend $before 'C:\x;evil\QQON-Panel' } '固定'
        Check ($env:PATH -ceq $script:ProcessPathBefore)
    }

    Test '只读status/url/logs拒绝lock/recover/exec，日志限量脱敏' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Assert-QPNativeHost { param([switch]$Administrator) Check (-not $Administrator) }
        function Get-QPPaths { return $paths }
        function Get-QPService { return @{ State='Stopped'; StartName='NT SERVICE\QQONPanel'; PathName=(Get-QPServiceCommand $paths $config) } }
        function Assert-QPService { param($Paths,$Config,$Service) Check ($Service.PathName -ceq (Get-QPServiceCommand $Paths $Config)) }
        function Assert-QPBinary { param($Path,$Config,[switch]$ReadOnly) Check $ReadOnly }
        function Assert-QPLayout { param($Paths) Assert-TestPath $Paths.Data }
        function Resume-QPTransaction { throw '不应恢复' }; function Invoke-QPProcess { throw '不应执行' }
        $paths = New-TestPaths; $config = New-TestConfig; Save-TestInstall $paths $config
        $logs = [IO.Path]::Combine($paths.Data,'logs'); [void][IO.Directory]::CreateDirectory($logs)
        $logPath = [IO.Path]::Combine($logs,'runtime.log')
        [IO.File]::WriteAllText($logPath, ('x' * 140000) + "`npassword=NEVER_PRINT_THIS`nnormal-one`nnormal-two`n")
        $output = Capture-Console { Invoke-QPMaintenance @('status'); Invoke-QPMaintenance @('url'); Invoke-QPMaintenance @('logs','--lines','3') }
        Check ($output.Contains('已停止') -and $output.Contains('未检测') -and $output.Contains('normal-two') -and -not $output.Contains('NEVER_PRINT_THIS'))
        Check ($output.Length -lt 131072 -and -not [IO.File]::Exists($paths.Journal))
    }

    Test 'config成功/失败/中断恢复原SCM与账户，原停止状态不变' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Get-QPService { return $fake }
        function Assert-QPService { param($Paths,$Config,$Service) Check ($Service.PathName -ceq (Get-QPServiceCommand $Paths $Config)) 'SCM命令不匹配' }
        function Assert-QPActiveTls { param($Paths,$Config,[switch]$Inspect) }
        function Stop-QPServiceAndWait { param($Paths,$Config) Assert-QPService $Paths $Config $fake; $fake.State='Stopped'; [void]$events.Add('stop') }
        function Start-QPService { param($Paths,$Config) Assert-QPService $Paths $Config $fake; $fake.State='Running'; [void]$events.Add('start') }
        function Set-QPServiceConfig { param($Paths,$Before,$After) Assert-QPService $Paths $Before $fake; $fake.PathName=Get-QPServiceCommand $Paths $After; [void]$events.Add('scm') }
        function Wait-QPHealthy { param($Paths,$Config) [void]$events.Add('health'); if ($fault.Fail -and $Config['listen'] -ceq '127.0.0.1:9443') { throw '模拟新配置健康失败' } }
        function Assert-QPLayout { param($Paths) Assert-TestPath $Paths.Data }
        foreach ($running in @($true,$false)) {
            $paths = New-TestPaths; $old = New-TestConfig; Save-TestInstall $paths $old
            $new = Set-QPAccessValue $old (Get-QPAccess local 9443) $null
            $fake = @{ State = $(if ($running) {'Running'} else {'Stopped'}); PathName = Get-QPServiceCommand $paths $old }
            $events = [Collections.Generic.List[string]]::new(); $fault=@{Fail=$false}
            [IO.File]::WriteAllText([IO.Path]::Combine($paths.Data,'account.json'),'ACCOUNT_SENTINEL')
            Invoke-QPConfigTransaction $paths $old $new
            Check ((Read-QPConfig $paths)['listen'] -ceq '127.0.0.1:9443' -and -not [IO.File]::Exists($paths.Journal))
            Check ($fake.State -ceq $(if($running){'Running'}else{'Stopped'}))
            $fault.Fail=$true; $fake.PathName=Get-QPServiceCommand $paths $old; Write-QPAtomicJson $paths.Config $old
            Refuses { Invoke-QPConfigTransaction $paths $old $new } '已恢复旧配置'
            Check ((Read-QPConfig $paths)['listen'] -ceq $old['listen'] -and $fake.PathName -ceq (Get-QPServiceCommand $paths $old))
            Check ([IO.File]::ReadAllText([IO.Path]::Combine($paths.Data,'account.json')) -ceq 'ACCOUNT_SENTINEL')
            $fake.PathName=Get-QPServiceCommand $paths $new
            Write-QPAtomicJson $paths.Journal @{format=2;kind='config';phase='ready';old=$old;new=$new;wasRunning=$running}
            Resume-QPTransaction $paths
            Check (-not [IO.File]::Exists($paths.Journal) -and $fake.PathName -ceq (Get-QPServiceCommand $paths $old))
        }
    }

    Test 'backup真实TEMP快照/私有ACL/排除锁/活动TLS/停止恢复/磁盘失败' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Get-QPService { return $fake }; function Assert-QPService { param($Paths,$Config,$Service) }
        function Stop-QPServiceAndWait { param($Paths,$Config) $fake.State='Stopped'; [void]$events.Add('stop') }
        function Start-QPService { param($Paths,$Config) $fake.State='Running'; [void]$events.Add('start') }
        function Wait-QPHealthy { param($Paths,$Config) [void]$events.Add('health') }
        foreach ($running in @($true,$false)) {
            $paths=New-TestPaths; $config=Set-QPAccessValue (New-TestConfig) (Get-QPAccess public-selfsigned 8443 8.8.8.8) (New-TestTls)
            Save-TestInstall $paths $config
            [IO.File]::WriteAllText([IO.Path]::Combine($paths.Data,'account.json'),'SECRET_ACCOUNT')
            [IO.File]::WriteAllText([IO.Path]::Combine($paths.Data,'.panel.lock'),'LOCK_SENTINEL')
            $tls=Get-QPTlsPaths $paths $config['tls']['generation']; [void][IO.Directory]::CreateDirectory($tls.Directory)
            [IO.File]::WriteAllText($tls.Certificate,'CERT'); [IO.File]::WriteAllText($tls.Key,'SECRET_KEY')
            $fake=@{State=$(if($running){'Running'}else{'Stopped'})}; $events=[Collections.Generic.List[string]]::new()
            $output=Capture-Console { Invoke-QPBackup $paths $config }
            Check ($fake.State -ceq $(if($running){'Running'}else{'Stopped'}) -and -not [IO.File]::Exists($paths.Journal))
            $backups=@([IO.Directory]::EnumerateDirectories([IO.Path]::Combine($paths.State,'backups'))); Check ($backups.Count -eq 1)
            $backup=$backups[0]
            Check ([IO.File]::ReadAllText([IO.Path]::Combine($backup,'data\account.json')) -ceq 'SECRET_ACCOUNT')
            Check (-not [IO.File]::Exists([IO.Path]::Combine($backup,'data\.panel.lock')))
            Check ([IO.File]::ReadAllText([IO.Path]::Combine($backup,'tls\key.pem')) -ceq 'SECRET_KEY')
            Check ([IO.Directory]::GetAccessControl($backup).AreAccessRulesProtected -and [IO.File]::GetAccessControl([IO.Path]::Combine($backup,'tls\key.pem')).AreAccessRulesProtected)
            Check (-not $output.Contains('SECRET_KEY') -and -not $output.Contains('SECRET_ACCOUNT'))
            if (-not $running) { Check (-not $events.Contains('start')) }
        }
        function New-QPBackupSnapshot { param($Paths,$Config,$Generation) throw '模拟磁盘写入失败' }
        $fake.State='Running'; $events.Clear()
        Refuses { Invoke-QPBackup $paths $config } '原服务状态已恢复'
        Check ($fake.State -ceq 'Running' -and -not [IO.File]::Exists($paths.Journal))
        function Start-QPService { param($Paths,$Config) throw '模拟服务恢复失败' }
        Refuses { Invoke-QPBackup $paths $config } '未能恢复原服务'
        Check ([IO.File]::Exists($paths.Journal) -and (Read-QPTransaction $paths)['kind'] -ceq 'backup')
    }

    Test '备份上限/普通文件和导入PEM长度检查' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        $paths=New-TestPaths; $source=[IO.Path]::Combine($paths.Data,'file'); [IO.File]::WriteAllText($source,'x')
        Refuses { Copy-QPBackupFile $source ([IO.Path]::Combine($paths.State,'too-many')) @{Count=10000;Size=[long]0} } '10000'
        Refuses { Copy-QPBackupFile $source ([IO.Path]::Combine($paths.State,'too-big')) @{Count=0;Size=[long]536870912} } '512'
        $cert=[IO.Path]::Combine($paths.State,'cert.pem'); $key=[IO.Path]::Combine($paths.State,'key.pem')
        [IO.File]::WriteAllText($cert,"-----BEGIN CERTIFICATE-----`nZmFrZQ==`n-----END CERTIFICATE-----`n")
        [IO.File]::WriteAllText($key,"-----BEGIN PRIVATE KEY-----`nZmFrZQ==`n-----END PRIVATE KEY-----`n")
        $pair=Read-QPPemPair $cert $key; Check ($pair.Count -eq 2)
        $alias=[IO.Path]::Combine($paths.State,'hardlink.pem')
        [void](New-Item -ItemType HardLink -Path $alias -Target $key)
        Refuses { Read-QPPemPair $cert $alias } 'QP_REGULAR'
        [IO.File]::Delete($alias)
        [IO.File]::WriteAllBytes($key,(New-Object byte[] 65537)); Refuses { Read-QPPemPair $cert $key } 'QP_LIMIT'
        Refuses { Read-QPPemPair $cert $paths.Data }
    }

    Test '已知官方v0.5 hash门槛/format1事务不能混用format2' {
        $script:LegacyInstaller = Read-LegacyAsset 'install.ps1'; $script:LegacyUpdater = Read-LegacyAsset 'update.ps1'
        Check ((Get-QPSha256 $script:LegacyInstaller) -ceq 'db0bf026d4debb409fd14b30f0837cc7ba4d5a3f48cfec121545feff8c3931c8')
        Check ((Get-QPSha256 $script:LegacyUpdater) -ceq '383e0eec3c969ed9dd93ee8e05c1ebc4fd2d12bb4162dda6300025cfe951278d')
        $old=New-LegacyConfig; Assert-QPKnownLegacy $old
        Refuses { Read-QPConfigValue $old } 'format2'
        $bad=$old.Clone(); $bad['installerSHA256']='f'*64; Refuses { Assert-QPKnownLegacy $bad } '官方'
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        $paths=New-TestPaths
        Write-QPAtomicJson $paths.Journal @{format=1;kind='install';phase='prepared';old=$null;new=(New-TestConfig)}
        Refuses { Read-QPTransaction $paths } '格式'
    }

    Test '官方旧版独立迁移真实backup/保留oldbinary和account/成功及失败rollback' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Assert-QPLayout { param($Paths) Assert-TestPath $Paths.Code }
        function Assert-QPBinary { param($Path,$Config,[switch]$ReadOnly) Check ((Get-QPFileHash $Path) -ceq $Config['binarySHA256']) }
        function Get-QPService { return $fake }; function Assert-QPService { param($Paths,$Config,$Service) Check ($Service.PathName -ceq (Get-QPServiceCommand $Paths $Config)) }
        function Stop-QPServiceAndWait { param($Paths,$Config) $fake.State='Stopped' }
        function Start-QPService { param($Paths,$Config) $fake.State='Running' }
        function Wait-QPHealthy { param($Paths,$Config) if($fault.Fail -and $Config['format'] -eq 2){throw '模拟新管理配置健康失败'} }
        function Get-QPReleaseMetadata { param($Version) return $metadata }
        function Read-QPVerifiedAsset { param($Metadata,$Name) return ,$assets[$Name] }
        foreach($failure in @($false,$true)) {
            $paths=New-TestPaths; $old=New-LegacyConfig
            [IO.File]::WriteAllBytes($paths.Installer,$script:LegacyInstaller); [IO.File]::WriteAllBytes($paths.Updater,$script:LegacyUpdater)
            [IO.File]::WriteAllBytes($paths.Binary,(Bytes 'app')); Write-QPAtomicJson $paths.Config $old
            [IO.File]::WriteAllText([IO.Path]::Combine($paths.Data,'account.json'),'KEEP_OLD_ACCOUNT')
            $assets=@{ 'install.ps1'=[IO.File]::ReadAllBytes([IO.Path]::Combine($script:Scripts,'install.ps1')); 'update.ps1'=[IO.File]::ReadAllBytes([IO.Path]::Combine($script:Scripts,'update.ps1')) }
            $metadata=@{Manifest=@{version='0.6.0'};Sums=@{'install.ps1'=(Get-QPSha256 $assets['install.ps1']);'update.ps1'=(Get-QPSha256 $assets['update.ps1'])}}
            $fake=@{State='Running';PathName=(Get-QPServiceCommand $paths $old)}; $fault=@{Fail=$failure}
            if($failure) { Refuses { Invoke-QPMigration $paths 'v0.6.0' ([IO.Path]::Combine($script:Scripts,'install.ps1')) } '旧配置.*已恢复' }
            else { Invoke-QPMigration $paths 'v0.6.0' ([IO.Path]::Combine($script:Scripts,'install.ps1')) }
            $current=Read-QPConfig $paths -AllowLegacy
            Check ($current['format'] -eq $(if($failure){1}else{2}) -and $current['version'] -ceq '0.5.0')
            Check ([IO.File]::ReadAllText($paths.Binary) -ceq 'app' -and [IO.File]::ReadAllText([IO.Path]::Combine($paths.Data,'account.json')) -ceq 'KEEP_OLD_ACCOUNT')
            Check (-not [IO.File]::Exists($paths.Journal) -and $fake.State -ceq 'Running')
            $backup=@([IO.Directory]::EnumerateDirectories([IO.Path]::Combine($paths.State,'backups'))); Check ($backup.Count -eq 1)
            Check ((Get-QPFileHash ([IO.Path]::Combine($backup[0],'install.ps1'))) -ceq $old['installerSHA256'])
            if($failure) { Assert-QPKnownLegacy $current; Check (-not [IO.File]::Exists([IO.Path]::Combine($paths.Code,'qqon.cmd'))) }
            else { Check ($current['accessMode'] -ceq 'local' -and $null -eq $current['tls']); Assert-QPShortcuts $paths }
        }
    }

    foreach ($crashTarget in @('install.ps1', 'update.ps1', 'rollback-update.ps1')) {
        Test ('迁移暂存完成但replace前崩溃可恢复：' + $crashTarget) {
            function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
            function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
            function Get-QPService { return $fake }
            function Assert-QPService { param($Paths,$Config,$Service) Check ($Service.PathName -ceq (Get-QPServiceCommand $Paths $Config)) }
            function Stop-QPServiceAndWait { param($Paths,$Config) Assert-QPService $Paths $Config $fake; $fake.State='Stopped' }
            function Start-QPService { param($Paths,$Config) Assert-QPService $Paths $Config $fake; $fake.State='Running' }
            function Wait-QPHealthy { param($Paths,$Config) Check ($Config['format'] -eq 1 -and $Config['version'] -ceq '0.5.0') }
            $fixture=New-MigrationCrashFixture; $paths=$fixture.Paths; $old=$fixture.Old; $transaction=$fixture.Transaction
            $name=$crashTarget; $bytes=$null
            if($crashTarget -ceq 'update.ps1') { [IO.File]::WriteAllBytes($paths.Installer,$fixture.Assets['install.ps1']) }
            if($crashTarget -ceq 'rollback-update.ps1') {
                $name='update.ps1'; $bytes=$script:LegacyUpdater
                [IO.File]::WriteAllBytes($paths.Updater,$fixture.Assets['update.ps1'])
                Write-QPAtomicJson $paths.Config $fixture.New
                $transaction['phase']='ready'; Write-QPAtomicJson $paths.Journal $transaction
            } else { $bytes=$fixture.Assets[$name] }
            # Persist exactly the state after the real temp writer flush, before Replace; no finally cleanup.
            $stage=Get-QPMigrationStagePath ([IO.Path]::Combine($paths.Code,$name)) $transaction['backup']
            Write-QPNewFile $stage $bytes 'Code'
            Check ([IO.File]::Exists($stage))
            Refuses { Assert-QPLayout $paths } 'QP_PATH'
            $fake=@{State='Stopped';PathName=(Get-QPServiceCommand $paths $old)}
            # A reviewed/bootstrap new manager recovers even while the application and an installed manager are still v0.5.
            Update-QPPanel $paths ''
            Check (-not [IO.File]::Exists($stage) -and -not [IO.File]::Exists($paths.Journal))
            Assert-QPLayout $paths
            $restored=Read-QPConfig $paths -AllowLegacy; Assert-QPKnownLegacy $restored
            Check ((Get-QPFileHash $paths.Installer 524288) -ceq $old['installerSHA256'])
            Check ((Get-QPFileHash $paths.Updater 32768) -ceq $old['updaterSHA256'])
            Check ($fake.State -ceq 'Running' -and [IO.File]::ReadAllText($paths.Binary) -ceq 'app')
            Check ([IO.File]::ReadAllText([IO.Path]::Combine($paths.Data,'account.json')) -ceq 'CRASH_ACCOUNT_SENTINEL')
            Check ([IO.Directory]::Exists((Get-QPBackupDirectory $paths $transaction['backup'])))
        }
    }

    Test '迁移清理拒绝未知文件/错误代次/错误摘要/非保护ACL且不先删合法暂存' {
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        function Stop-QPServiceAndWait { throw '拒绝前不得停止或启动服务' }
        foreach($invalid in @('unknown','generation','hash','acl','target')) {
            $fixture=New-MigrationCrashFixture; $paths=$fixture.Paths; $transaction=$fixture.Transaction
            $stage=Get-QPMigrationStagePath $paths.Installer $transaction['backup']
            Write-QPNewFile $stage $fixture.Assets['install.ps1'] 'Code'
            $extra=$null
            switch($invalid) {
                'unknown' { $extra=[IO.Path]::Combine($paths.Code,'unknown.txt'); Write-QPNewFile $extra (Bytes 'unknown') 'Code' }
                'generation' { $extra=Get-QPMigrationStagePath $paths.Updater ([guid]::NewGuid().ToString('N')); Write-QPNewFile $extra $fixture.Assets['update.ps1'] 'Code' }
                'hash' { [IO.File]::WriteAllText($stage,'wrong-stage-content') }
                'acl' { $acl=[IO.File]::GetAccessControl($stage); $acl.SetAccessRuleProtection($false,$true); [IO.File]::SetAccessControl($stage,$acl) }
                'target' { [IO.File]::WriteAllText($paths.Updater,'wrong-target-content') }
            }
            $before=Get-QPFileHash $stage
            Refuses { Resume-QPTransaction $paths } 'QP_MIGRATION_STAGE'
            Check ([IO.File]::Exists($stage) -and (Get-QPFileHash $stage) -ceq $before)
            Check ([IO.File]::Exists($paths.Journal))
            if($extra){Check ([IO.File]::Exists($extra))}
            Check ([IO.File]::ReadAllText([IO.Path]::Combine($paths.Data,'account.json')) -ceq 'CRASH_ACCOUNT_SENTINEL')
        }
    }

    Test '真实TLS证书pin/SAN/有效期与loopback HTTPS错误证书拒绝' {
        Initialize-QPHttpsProbe
        if (-not $PanelBinary) { $PanelBinary = [IO.Path]::GetFullPath([IO.Path]::Combine($script:Scripts, '..\bin\QQON-Panel-v0.6.0-maintenance-fixture.exe')) }
        Check ([IO.File]::Exists($PanelBinary)) '请通过 -PanelBinary 提供已构建 v0.6 测试程序；测试不下载或执行网络代码。'
        $paths=New-TestPaths
        [IO.File]::Copy($PanelBinary,$paths.Binary)
        $info=ConvertFrom-QPJson (Invoke-QPProcess $paths.Binary @('-build-info'))
        $config=New-TestConfig
        foreach($field in @('version','commit','buildTime','repository','dataSchema')){$config[$field]=$info[$field]}
        $config['binarySize']=([IO.FileInfo]::new($paths.Binary)).Length; $config['binarySHA256']=Get-QPFileHash $paths.Binary
        function Assert-QPAcl { param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path }
        function New-QPSecurity { param($Kind,[switch]$File) return New-TestSecurity -File:$File }
        # Exercise the real production create + inspect adapter and ACL transition using TEMP-only owner substitutions.
        $access=Get-QPAccess public-selfsigned 8443 8.8.8.8
        $tls=New-QPTlsGeneration $paths $config $access $paths.Binary
        $files=Get-QPTlsPaths $paths $tls['generation']
        $cert=[Security.Cryptography.X509Certificates.X509Certificate2]::new($files.Certificate)
        try {
            $pin=Get-QPSha256 $cert.RawData; $expiry=$cert.NotAfter.ToUniversalTime(); $now=[datetime]::UtcNow
            Check ($pin -ceq $tls['fingerprint'])
            Check ([QQONPanel.PinnedHttps]::Validate($cert,'8.8.8.8',$pin,$expiry,$now))
            Check (-not [QQONPanel.PinnedHttps]::Validate($cert,'other.example.com',$pin,$expiry,$now))
            Check (-not [QQONPanel.PinnedHttps]::Validate($cert,'8.8.8.8',('f'*64),$expiry,$now))
            Check (-not [QQONPanel.PinnedHttps]::Validate($cert,'8.8.8.8',$pin,$expiry,$expiry.AddSeconds(1)))
            Check (-not [QQONPanel.PinnedHttps]::Validate($cert,'8.8.8.8',$pin,$expiry,$cert.NotBefore.ToUniversalTime().AddSeconds(-1)))
        } finally {$cert.Dispose()}
        $listener=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0); $listener.Start()
        $port=([Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()
        $config=Set-QPAccessValue $config (Get-QPAccess public-selfsigned $port 8.8.8.8) $tls
        $start=[Diagnostics.ProcessStartInfo]::new()
        $start.FileName=$paths.Binary; $start.WorkingDirectory=[Environment]::SystemDirectory
        $start.UseShellExecute=$false; $start.CreateNoWindow=$true; $start.RedirectStandardOutput=$true; $start.RedirectStandardError=$true
        $arguments=@('-listen',('127.0.0.1:'+$port),'-origin',$config['origin'],'-tls-cert',$files.Certificate,'-tls-key',$files.Key,'-data-dir',$paths.Data,'-runtime-log=false')
        $start.Arguments=(@($arguments | ForEach-Object {ConvertTo-QPArgument $_}) -join ' ')
        $process=[Diagnostics.Process]::Start($start)
        $stdout=$process.StandardOutput.ReadToEndAsync(); $stderr=$process.StandardError.ReadToEndAsync()
        try {
            $ok=$false; $clock=[Diagnostics.Stopwatch]::StartNew()
            while($clock.Elapsed.TotalSeconds -lt 15 -and -not $process.HasExited){if(Test-QPHealth $config){$ok=$true;break};Start-Sleep -Milliseconds 100}
            if (-not $ok) {
                if ($process.HasExited) { throw ('测试主程序退出：' + $stderr.GetAwaiter().GetResult()) }
                try { $raw=[QQONPanel.PinnedHttps]::Probe($port,$tls['host'],$config['healthHost'],$tls['fingerprint'],(Read-QPUtcTime $tls['notAfter']).UtcDateTime); throw ('健康JSON不符：' + [Text.Encoding]::UTF8.GetString($raw)) }
                catch { throw $_.Exception.ToString() }
            }
            $bad=$config.Clone();$bad['tls']=$tls.Clone();$bad['tls']['fingerprint']='0'*64
            Check (-not(Test-QPHealth $bad)) '错误指纹必须拒绝'
            $bad=$config.Clone();$bad['tls']=$tls.Clone();$bad['tls']['host']='1.1.1.1';$bad['healthHost']='1.1.1.1:'+$port
            Check (-not(Test-QPHealth $bad)) '错误SAN主机必须拒绝'
            $bad=$config.Clone();$bad['tls']=$tls.Clone();$bad['tls']['notAfter']=[datetime]::UtcNow.AddDays(-1).ToString("yyyy-MM-dd'T'HH:mm:ss'Z'")
            Check (-not(Test-QPHealth $bad)) '错误有效期必须拒绝'
        } finally {
            if(-not $process.HasExited){$process.Kill();[void]$process.WaitForExit(5000)}
            [void]$stdout.GetAwaiter().GetResult();[void]$stderr.GetAwaiter().GetResult();$process.Dispose()
        }
        # Real import copies a bounded regular PEM pair into a different fixed generation, never persists source paths.
        $importAccess=Get-QPAccess public-certificate $port 8.8.8.8
        $import=New-QPTlsGeneration $paths $config $importAccess $paths.Binary $files.Certificate $files.Key
        Check ($import['generation'] -cne $tls['generation'] -and $import['fingerprint'] -ceq $tls['fingerprint'])
        Check (-not ([Text.Encoding]::UTF8.GetString((ConvertTo-QPJsonBytes $import))).Contains($files.Directory))
        # Real renewal creates a new immutable generation; fake SCM only records stop/switch/start/health.
        function Get-QPService {return $fake}
        function Assert-QPService {param($Paths,$Config,$Service) Check($Service.PathName -ceq (Get-QPServiceCommand $Paths $Config))}
        function Stop-QPServiceAndWait {param($Paths,$Config)$fake.State='Stopped'}
        function Start-QPService {param($Paths,$Config)$fake.State='Running'}
        function Set-QPServiceConfig {param($Paths,$Before,$After)$fake.PathName=Get-QPServiceCommand $Paths $After}
        function Wait-QPHealthy {param($Paths,$Config) Assert-QPActiveTls $Paths $Config -Inspect}
        $fake=@{State='Running';PathName=(Get-QPServiceCommand $paths $config)}
        Write-QPAtomicJson $paths.Config $config
        $before=Get-QPFileHash $files.Key
        Invoke-QPConfigure $paths $config -Renew -Yes:$true
        $renewed=Read-QPConfig $paths
        Check ($renewed['tls']['generation'] -cne $tls['generation'] -and $renewed['tls']['fingerprint'] -cne $tls['fingerprint'])
        Check ((Get-QPFileHash $files.Key) -ceq $before -and -not[IO.File]::Exists($paths.Journal))
        $legacy=$config.Clone();$legacy['version']='0.5.0'
        Refuses {New-QPTlsGeneration $paths $legacy $access $paths.Binary} '先 qqon update'
        $imported=Set-QPAccessValue $config $importAccess $import
        Refuses {Invoke-QPConfigure $paths $imported -Renew -Yes:$true} '仅支持自签'
        Check ([Net.ServicePointManager]::ServerCertificateValidationCallback -eq $script:CallbackBefore)
    }

    Test '所有实际测试未改变宿主执行策略/PATH/global TLS callback' {
        Check ((Get-ExecutionPolicy -List | Out-String) -ceq $script:PolicyBefore)
        Check ($env:PATH -ceq $script:ProcessPathBefore)
        Check ([Net.ServicePointManager]::ServerCertificateValidationCallback -eq $script:CallbackBefore)
    }
} finally {
    if([IO.Directory]::Exists($script:Root)){[IO.Directory]::Delete($script:Root,$true)}
}
[Console]::WriteLine(('Windows maintenance tests: {0} passed, {1} failed; native PS5.1, TEMP/fake SCM/PATH only; HTTPS loopback only.' -f $script:Passed,$script:Failed))
if($script:Failed -gt 0){exit 1}
