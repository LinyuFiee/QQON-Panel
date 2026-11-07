# v0.7 Windows privilege tests: only new TEMP directories, pure ACL descriptors and mocked SCM.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
$script:Scripts = [IO.Path]::GetFullPath([IO.Path]::Combine($PSScriptRoot, '..'))
$script:Root = [IO.Path]::Combine([IO.Path]::GetTempPath(), 'QQON-privilege-tests-' + [guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($script:Root)
$script:Passed = 0; $script:Failed = 0
$script:Policy = Get-ExecutionPolicy -List | Out-String
$script:OriginalPath = $env:PATH
function Check { param([bool]$Condition, [string]$Message = 'Assertion failed'); if (-not $Condition) { throw $Message } }
function Refuses { param([scriptblock]$Body, [string]$Pattern = '.'); $caught=$false; try { & $Body | Out-Null } catch { $caught=$true; Check ($_.Exception.Message -match $Pattern) ('Wrong refusal: ' + $_.Exception.Message) }; Check $caught 'Expected refusal' }
function Test { param([string]$Name, [scriptblock]$Body); try { & $Body; $script:Passed++; [Console]::WriteLine('PASS '+$Name) } catch { $script:Failed++; [Console]::Error.WriteLine('FAIL '+$Name+': '+$_.Exception.Message+"`n"+$_.ScriptStackTrace) } }
function Bytes { param([string]$Value); return ,([Text.UTF8Encoding]::new($false).GetBytes($Value)) }
function Assert-TestPath { param([string]$Path); Check ($Path.StartsWith($script:Root+'\', [StringComparison]::OrdinalIgnoreCase)) ('Non-TEMP access: '+$Path); Assert-QPPlainPath $Path -AllowMissing }
function New-TestSecurity {
    param([switch]$File)
    if ($File) { $acl=[Security.AccessControl.FileSecurity]::new() } else { $acl=[Security.AccessControl.DirectorySecurity]::new() }
    $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl.SetOwner($sid); $acl.SetAccessRuleProtection($true,$false)
    $inherit=[Security.AccessControl.InheritanceFlags]::None
    if (-not $File) { $inherit=[Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit' }
    foreach ($id in @($sid,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'),[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) { $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($id,[Security.AccessControl.FileSystemRights]::FullControl,$inherit,[Security.AccessControl.PropagationFlags]::None,[Security.AccessControl.AccessControlType]::Allow)) }
    return $acl
}
function New-Consent { return @{format=1;mode='system';identity='LocalSystem';noticeVersion=1;acceptedAt=[datetime]::UtcNow.AddMinutes(-1).ToString("yyyy-MM-dd'T'HH:mm:ss'Z'");acceptedBy='Administrator';notice='full-system-management'} }
function New-Config {
    return @{format=3;privilegeMode='restricted';privilegeConsent=$null;repository='LinyuFiee/QQON-Panel';service='QQONPanel';listen='127.0.0.1:8443';origin='';healthHost='127.0.0.1:8443';accessMode='local';tls=$null;version='0.7.0';dataSchema=1;commit='c'*40;buildTime='2026-11-07T00:00:00Z';binarySize=3;binarySHA256=(Get-QPSha256 (Bytes 'app'));installerSHA256='a'*64;updaterSHA256='b'*64}
}
function New-Paths {
    $base=[IO.Path]::Combine($script:Root,[guid]::NewGuid().ToString('N')); [void][IO.Directory]::CreateDirectory($base)
    $code=$base+'\code';$state=$base+'\state';$data=$state+'\data'
    foreach($path in @($code,$state,$data)){[IO.DirectoryInfo]::new($path).Create((New-TestSecurity))}
    return @{Code=$code;State=$state;Data=$data;Binary=$code+'\panel.exe';Installer=$code+'\install.ps1';Updater=$code+'\update.ps1';Config=$state+'\install.json';Journal=$state+'\transaction.json';Candidate=$code+'\panel.next.exe';Previous=$code+'\panel.previous.exe'}
}
function New-InstalledFixture {
    $paths=New-Paths; $config=New-Config
    [IO.File]::WriteAllText($paths.Binary,'app')
    foreach($name in @('install.ps1','update.ps1')){[IO.File]::Copy([IO.Path]::Combine($script:Scripts,$name),[IO.Path]::Combine($paths.Code,$name))}
    $config['installerSHA256']=Get-QPFileHash $paths.Installer 524288; $config['updaterSHA256']=Get-QPFileHash $paths.Updater 32768
    Write-QPAtomicJson $paths.Config $config; Write-QPShortcuts $paths
    foreach($folder in @('accounts\nested','accounts\totp','logs')){[void][IO.Directory]::CreateDirectory([IO.Path]::Combine($paths.Data,$folder))}
    foreach($name in @('accounts\nested\account.json','accounts\totp\state.json','logs\runtime.log')){[IO.File]::WriteAllText([IO.Path]::Combine($paths.Data,$name),'KEEP_'+$name)}
    $tls=Get-QPTlsPaths $paths ('1'*32); [void][IO.Directory]::CreateDirectory($tls.Directory)
    [IO.File]::WriteAllText($tls.Certificate,'CERT');[IO.File]::WriteAllText($tls.Key,'PRIVATE_KEY_SENTINEL')
    return @{Paths=$paths;Config=$config;Service=@{Name='QQONPanel';DisplayName='QQON-Panel';StartName='NT SERVICE\QQONPanel';ServiceType='Own Process';StartMode='Auto';State='Running';PathName=(Get-QPServiceCommand $paths $config)}}
}
try {
    . ([IO.Path]::Combine($script:Scripts,'install.ps1'))
    . ([IO.Path]::Combine($script:Scripts,'update.ps1'))
    $script:RealSetEntry=${function:Set-QPEntrySecurity}
    $script:RealDescriptor=${function:Assert-QPPrivilegeDescriptor}
    function Get-QPPaths { throw 'Host paths forbidden' }
    function Get-CimInstance { throw 'Real SCM forbidden' }
    function New-QPServiceController { throw 'Real SCM forbidden' }
    function Get-QPReleaseMetadata { throw 'Network forbidden' }
    function Read-QPDownload { throw 'Network forbidden' }
    function Add-QPSystemPath { throw 'Host PATH write forbidden' }
    function Enter-QPLock { throw 'Global mutex forbidden' }
    function Enter-QPPrivilegeSecurity { throw 'Host token privilege mutation forbidden' }

    Test 'separate consent: Yes/redirected input never authorizes; exact Chinese phrase only' {
        function Test-QPInteractiveInput { return $true }
        function Read-QPConsole { return $phrase }
        Check ($null -eq (Confirm-QPSystemManagement $false $true))
        foreach($phrase in @('确认','yes',' 同意最高权限','同意最高权限 ')){Check ($null -eq (Confirm-QPSystemManagement $false $false))}
        $phrase='同意最高权限'; $record=Confirm-QPSystemManagement $false $false
        Check ($record['identity'] -ceq 'LocalSystem' -and $record['notice'] -ceq 'full-system-management')
        Check ($null -ne (Confirm-QPSystemManagement $true $true))
        function Test-QPInteractiveInput { return $false }
        Check ($null -eq (Confirm-QPSystemManagement $false $false))
    }
    Test 'format3 exact consent and old format2 defaults restricted' {
        $config=New-Config; [void](Read-QPConfigValue $config)
        Check ((Read-QPWrapperIdentity (ConvertTo-QPJsonBytes $config)).privilegeMode -ceq 'restricted')
        $config['privilegeMode']='system';$config['privilegeConsent']=New-Consent
        [void](Read-QPConfigValue $config); [void](Read-QPWrapperIdentity (ConvertTo-QPJsonBytes $config))
        foreach($time in @('2026-11-07T00:00:00+00:00','2026-11-07T00:00:00.000Z','2026-02-31T00:00:00Z','2099-01-01T00:00:00Z')){$bad=New-Consent;$bad['acceptedAt']=$time;Refuses {Read-QPPrivilegeConsent $bad}}
        $bad=New-Consent;$bad['path']='C:\arbitrary';Refuses {Read-QPPrivilegeConsent $bad}
        $legacy=New-Config;$legacy['format']=2;$legacy.Remove('privilegeMode');$legacy.Remove('privilegeConsent');[void](Read-QPConfigValue $legacy)
        Check ((Get-QPPrivilegeMode $legacy) -ceq 'restricted')
        $text=[Text.Encoding]::UTF8.GetString((ConvertTo-QPJsonBytes $config));$text=$text.Replace('"noticeVersion":1','"noticeVersion":1,"NoticeVersion":1')
        Refuses {Read-QPWrapperIdentity (Bytes $text)} '重复'
    }
    Test 'privilege CLI and wrapper whitelist parity; update cannot accept privilege flag' {
        foreach($words in @(@('privilege'),@('privilege','--yes'),@('privilege','--accept-system-management','--yes'))){$a=Read-QPMaintenanceCommand $words;$b=Read-QPWrapperCommand $words;foreach($key in @('Action','Yes','AcceptSystemManagement')){Check ($a[$key] -ceq $b[$key])}}
        foreach($words in @(@('update','--accept-system-management'),@('privilege','--accept-system-management','--accept-system-management'),@('privilege','--version','v0.7.0'))){Refuses {Read-QPMaintenanceCommand $words};Refuses {Read-QPWrapperCommand $words}}
        Refuses {Invoke-QPInstaller @{Action='Update';AcceptSystemManagement=$true} 'unused'} 'privilege'
        Refuses {Invoke-QPInstaller @{Action='Migrate';AcceptSystemManagement=$true} 'unused'} 'privilege'
        function Invoke-QPMaintenance {param($Words) $script:Captured=$Words}
        Invoke-QPInstaller @{Maintenance='privilege';AcceptSystemManagement=$true;Yes=$true} 'unused'
        Check (($script:Captured -join ' ') -ceq 'privilege --accept-system-management --yes')
    }
    Test 'bootstrap rejects accept switch for old metadata before download or temp writes' {
        . ([IO.Path]::Combine($script:Scripts,'bootstrap.ps1'))
        function Assert-QPNativeHost {param([switch]$Administrator)}
        function Get-QPReleaseMetadata {return @{Manifest=@{version='0.6.0'}}}
        function Read-QPVerifiedAsset {throw 'UNEXPECTED DOWNLOAD'}
        Refuses {Invoke-QPBootstrap $true} 'v0.7.0'
    }
    Test 'system SCM identity is exact LocalSystem and mismatched legacy identity rejected' {
        $paths=New-Paths;$old=New-Config;$new=$old.Clone();$new['privilegeMode']='system';$new['privilegeConsent']=New-Consent
        $service=@{Name='QQONPanel';DisplayName='QQON-Panel';StartName='NT SERVICE\QQONPanel';ServiceType='Own Process';StartMode='Auto';PathName=(Get-QPServiceCommand $paths $old)}
        Assert-QPServiceDefinition $paths $old $service;Refuses {Assert-QPServiceDefinition $paths $new $service}
        $service.StartName='LocalSystem';Assert-QPServiceDefinition $paths $new $service;Refuses {Assert-QPServiceDefinition $paths $old $service}
        $calls=[Collections.Generic.List[object]]::new()
        function Get-QPService {return $null}
        function Assert-QPSystemConsent {param($Paths,$Config) [void]$calls.Add(@('consent'))}
        function Invoke-QPProcess {param($File,$Arguments) [void]$calls.Add($Arguments);if($Arguments[0] -ceq 'sidtype'){throw 'MOCK END'}}
        Refuses {Register-QPService $paths $new} 'MOCK END'
        Check ($calls[0][0] -ceq 'consent' -and $calls[1] -ccontains 'LocalSystem')
    }
    Test 'production Admin ACL has only SYSTEM/Administrators and old descriptor validates' {
        foreach($file in @($false,$true)){$acl=New-QPSecurity Admin -File:$file;Check $acl.AreAccessRulesProtected;$rules=@($acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]));Check ($rules.Count -eq 2);foreach($rule in $rules){Check ($rule.IdentityReference.Value -cin @('S-1-5-18','S-1-5-32-544') -and $rule.FileSystemRights -eq [Security.AccessControl.FileSystemRights]::FullControl)}}
        $acl=New-QPSecurity Data -File;$acl.SetOwner((Get-QPServiceSid));$entry=@{scope='data';relative='account.json';directory=$false;sddl=$acl.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]'Owner, Group, Access')}
        Assert-QPPrivilegeDescriptor $entry
        $entry['sddl']='O:BAD:P(A;;FA;;;WD)';Refuses {Assert-QPPrivilegeDescriptor $entry}
    }

    Test 'restore privilege adapter compiles without changing process token privileges' {
        $tokens=$null;$errors=$null;$ast=[Management.Automation.Language.Parser]::ParseFile([IO.Path]::Combine($script:Scripts,'install.ps1'),[ref]$tokens,[ref]$errors)
        Check ($errors.Count -eq 0)
        $fn=$ast.Find({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -ceq 'Enter-QPPrivilegeSecurity'},$true)
        $source=$fn.Find({param($n) $n -is [Management.Automation.Language.StringConstantExpressionAst] -and $n.Value.StartsWith('using System;')},$true)
        Check ($null -ne $source)
        Add-Type -TypeDefinition $source.Value -ErrorAction Stop
        Check ($null -ne ('QQONPanel.RestorePrivilegeScope' -as [type]))
        # Do not construct the adapter or enable SeRestorePrivilege on this host.
    }

    # All following I/O and ACL writes are constrained to this run's TEMP root.
    function Assert-QPAcl {param($Path,$Kind,[switch]$File,[switch]$Root) Assert-TestPath $Path}
    function New-QPSecurity {param($Kind,[switch]$File) return New-TestSecurity -File:$File}
    function Assert-QPPrivilegeDescriptor {param($Entry) $d=[Security.AccessControl.RawSecurityDescriptor]::new($Entry['sddl']);Check ($d.Owner.Value -ceq [Security.Principal.WindowsIdentity]::GetCurrent().User.Value)}
    function Enter-QPPrivilegeSecurity { $s=[pscustomobject]@{};$s|Add-Member ScriptMethod Dispose {};return $s }
    function Get-QPService {return $fake}
    function Assert-QPService {param($Paths,$Config,$Service) Assert-QPServiceDefinition $Paths $Config $Service}
    function Assert-QPBinary {param($Path,$Config,[switch]$ReadOnly) Assert-TestPath $Path;Check ((Get-QPFileHash $Path) -ceq $Config['binarySHA256'])}
    function Stop-QPServiceAndWait {param($Paths,$Config) Assert-QPService $Paths $Config $fake;[void]$events.Add('stop');$fake.State='Stopped'}
    function Start-QPService {param($Paths,$Config) Assert-QPService $Paths $Config $fake;Assert-QPSystemConsent $Paths $Config;[void]$events.Add('start-'+(Get-QPPrivilegeMode $Config));$fake.State='Running'}
    function Wait-QPHealthy {param($Paths,$Config) [void]$events.Add('health-'+(Get-QPPrivilegeMode $Config));if($fault -ceq 'health' -and (Get-QPPrivilegeMode $Config) -ceq 'system'){[IO.File]::WriteAllText([IO.Path]::Combine($Paths.Data,'logs\new.log'),'PRESERVE_NEW_RUNTIME');throw 'INJECT HEALTH'}}
    function Invoke-QPProcess {
        param($File,$Arguments)
        Check ([IO.Path]::GetFileName($File) -ceq 'sc.exe' -and $Arguments[0] -ceq 'config' -and $Arguments[2] -ceq 'obj=') 'Only mock identity switch allowed'
        [void]$events.Add('identity-'+$Arguments[3]);$fake.StartName=$Arguments[3]
        if($fault -ceq 'identity' -and $fake.StartName -ceq 'LocalSystem'){throw 'INJECT IDENTITY AFTER SCM'}
        return ,(Bytes 'mock')
    }
    function Set-QPEntrySecurity {
        param($Path,$Directory,$Sddl='',$Kind='Admin')
        Assert-TestPath $Path
        if (-not $Sddl -and $Kind -ceq 'Admin') {Check ([IO.File]::Exists((Get-QPPrivilegeJournalPath $paths))) 'No ACL change before journal';[void]$events.Add('acl');if($fault -ceq 'acl' -and $Path.EndsWith('accounts\nested\account.json')){throw 'INJECT ACL'}}
        & $script:RealSetEntry $Path $Directory $Sddl $Kind
    }
    foreach($failure in @('none','acl','identity','health')) {
        Test ('privilege transaction real nested snapshot and rollback: '+$failure) {
            $fixture=New-InstalledFixture;$paths=$fixture.Paths;$old=$fixture.Config;$fake=$fixture.Service;$events=[Collections.Generic.List[string]]::new();$fault=$failure
            $before=[IO.File]::ReadAllBytes($paths.Config);$beforeSddl=@{}
            foreach($entry in (Get-QPPrivilegeEntries $paths)){$p=Get-QPPrivilegeEntryPath $paths $entry;if($entry['directory']){$a=[IO.Directory]::GetAccessControl($p)}else{$a=[IO.File]::GetAccessControl($p)};$beforeSddl[$p]=$a.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]'Owner, Group, Access')}
            if($failure -ceq 'none'){Invoke-QPPrivilege $paths $old (New-Consent)}else{Refuses {Invoke-QPPrivilege $paths $old (New-Consent)} '已恢复旧 owner/DACL'}
            Check (-not [IO.File]::Exists((Get-QPPrivilegeJournalPath $paths)))
            $current=Read-QPConfig $paths;Check ($current['privilegeMode'] -ceq $(if($failure -ceq 'none'){'system'}else{'restricted'}))
            Check ($fake.StartName -ceq $(if($failure -ceq 'none'){'LocalSystem'}else{'NT SERVICE\QQONPanel'}) -and $fake.State -ceq 'Running')
            Check ([IO.File]::ReadAllText([IO.Path]::Combine($paths.Data,'accounts\totp\state.json')) -ceq 'KEEP_accounts\totp\state.json')
            $backups=@([IO.Directory]::EnumerateDirectories([IO.Path]::Combine($paths.State,'privilege-backups')));Check ($backups.Count -eq 1)
            $snapshot=ConvertFrom-QPJson (Read-QPFile ([IO.Path]::Combine($backups[0],'snapshot.json')) 16777216) 16777216
            Check (@($snapshot['entries']|Where-Object {$_['scope'] -ceq 'tls' -and $_['relative'].EndsWith('key.pem')}).Count -eq 1)
            Check (@($snapshot['entries']|Where-Object {$_['relative'] -ceq 'accounts\totp\state.json'}).Count -eq 1)
            if($failure -cne 'none'){
                Check ((Get-QPSha256 ([IO.File]::ReadAllBytes($paths.Config))) -ceq (Get-QPSha256 $before)) 'Config raw bytes changed'
                Check (-not [IO.File]::Exists((Get-QPConsentPath $paths)))
                foreach($p in $beforeSddl.Keys){if([IO.Directory]::Exists($p)){$a=[IO.Directory]::GetAccessControl($p)}else{$a=[IO.File]::GetAccessControl($p)};Check (Test-QPSameSecurityDescriptor $beforeSddl[$p] ($a.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]'Owner, Group, Access'))) ('ACL not restored: '+$p+' expected='+$beforeSddl[$p]+' actual='+$a.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]'Owner, Group, Access'))}
                if($failure -ceq 'health'){Check ([IO.File]::ReadAllText([IO.Path]::Combine($paths.Data,'logs\new.log')) -ceq 'PRESERVE_NEW_RUNTIME')}
            }else{Assert-QPSystemConsent $paths $current;Check ($events.IndexOf('stop') -lt $events.IndexOf('acl'));Check ($events.IndexOf('acl') -lt $events.IndexOf('identity-LocalSystem'))}
        }
    }
    Test 'crash recovery restores prior consent bytes, stopped state and original ACL' {
        $fixture=New-InstalledFixture;$paths=$fixture.Paths;$old=$fixture.Config;$fake=$fixture.Service;$fake.State='Stopped';$events=[Collections.Generic.List[string]]::new();$fault='none'
        $oldConsent=New-Consent;$oldConsent['acceptedAt']='2026-01-01T00:00:00Z';Write-QPAtomicJson (Get-QPConsentPath $paths) $oldConsent
        $original=[IO.File]::ReadAllBytes((Get-QPConsentPath $paths));$new=$old.Clone();$new['privilegeMode']='system';$new['privilegeConsent']=New-Consent
        $transaction=@{format=1;kind='privilege';phase='prepared';old=$old;new=$new;wasRunning=$false;backup=[guid]::NewGuid().ToString('N');snapshotSHA256=$null;oldConsent=$oldConsent}
        Write-QPAtomicJson (Get-QPPrivilegeJournalPath $paths) $transaction
        $transaction['snapshotSHA256']=New-QPPrivilegeSnapshot $paths $transaction;$transaction['phase']='backed-up';Write-QPAtomicJson (Get-QPPrivilegeJournalPath $paths) $transaction
        foreach($entry in (Read-QPPrivilegeSnapshot $paths $transaction)['entries']){Set-QPEntrySecurity (Get-QPPrivilegeEntryPath $paths $entry) $entry['directory']}
        Write-QPManagementConsent $paths $new['privilegeConsent'];Set-QPPrivilegeIdentity $paths $old $new;Write-QPAtomicJson $paths.Config $new
        Resume-QPPrivilege $paths
        Check ($fake.State -ceq 'Stopped' -and $fake.StartName -ceq 'NT SERVICE\QQONPanel')
        Check ((Get-QPSha256 ([IO.File]::ReadAllBytes((Get-QPConsentPath $paths)))) -ceq (Get-QPSha256 $original))
    }
    Test 'ordinary update locks privilege and record; no permission changes on old format2' {
        $paths=New-Paths;$old=New-Config;$new=$old.Clone();$new['version']='0.7.1';$new['binarySHA256']='d'*64;$new['privilegeMode']='system';$new['privilegeConsent']=New-Consent
        Refuses {Invoke-QPUpdateTransaction $paths $old $new} '普通升级'
        Check (-not [IO.File]::Exists($paths.Journal))
        foreach($format in @(2,3)){$left=New-Config;$left['format']=$format;if($format -eq 2){$left.Remove('privilegeMode');$left.Remove('privilegeConsent')};$right=$left.Clone();$right['version']='0.7.1';$right['binarySHA256']='d'*64;Write-QPAtomicJson $paths.Journal @{format=$format;kind='update';phase='prepared';old=$left;new=$right};$j=Read-QPTransaction $paths;Check ((Get-QPPrivilegeMode $j['new']) -ceq 'restricted');Remove-QPManagedFile $paths.Journal 'Admin'}
        Refuses {Invoke-QPPrivilege $paths $left $null} '没有独立'
    }
    Test 'hardlink data and foreign snapshot paths rejected before ACL mutation' {
        $fixture=New-InstalledFixture;$paths=$fixture.Paths;$old=$fixture.Config;$fake=$fixture.Service;$events=[Collections.Generic.List[string]]::new();$fault='none'
        $link=[IO.Path]::Combine($paths.Data,'linked.json');[void](New-Item -ItemType HardLink -Path $link -Target ([IO.Path]::Combine($paths.Data,'accounts\nested\account.json')))
        Refuses {Invoke-QPPrivilege $paths $old (New-Consent)} '已恢复旧'
        Check (-not $events.Contains('acl') -and $fake.StartName -ceq 'NT SERVICE\QQONPanel')
        Refuses {Get-QPPrivilegeEntryPath $paths @{scope='data';relative='..\escape';directory=$false}}
        Refuses {Get-QPPrivilegeEntryPath $paths @{scope='state';relative='other.json';directory=$false}}
    }
    Test 'known official v0.6 manager pins, TLS preserved and no automatic privilege grant' {
        $old=New-Config;$old['format']=2;$old.Remove('privilegeMode');$old.Remove('privilegeConsent');$old['version']='0.6.0'
        $old['installerSHA256']='eb21039d21cd5aa29b4c75ae8eb14c1424ef76e08c89a233dd05c0c30c648168';$old['updaterSHA256']='84a262f5aa6305d1053ad043976fa6bc544bfec5ba3d5ca3632f33d889a7f133'
        Assert-QPKnownLegacy $old;$bad=$old.Clone();$bad['updaterSHA256']='f'*64;Refuses {Assert-QPKnownLegacy $bad}
        $new=$old.Clone();$new['format']=3;$new['privilegeMode']='restricted';$new['privilegeConsent']=$null;$new['installerSHA256']='d'*64;$new['updaterSHA256']='e'*64
        $paths=New-Paths;Write-QPAtomicJson $paths.Journal @{format=3;kind='migrate';phase='prepared';old=$old;new=$new;wasRunning=$false;backup=[guid]::NewGuid().ToString('N')}
        [void](Read-QPTransaction $paths)
        $new['privilegeMode']='system';$new['privilegeConsent']=New-Consent;Write-QPAtomicJson $paths.Journal @{format=3;kind='migrate';phase='prepared';old=$old;new=$new;wasRunning=$false;backup=[guid]::NewGuid().ToString('N')}
        Refuses {Read-QPTransaction $paths} '不是最高权限|最高权限模式要求'
    }
    Test 'host execution policy and process PATH unchanged' {Check ((Get-ExecutionPolicy -List|Out-String) -ceq $script:Policy);Check ($env:PATH -ceq $script:OriginalPath)}
} finally {if([IO.Directory]::Exists($script:Root)){[IO.Directory]::Delete($script:Root,$true)}}
[Console]::WriteLine(('Windows privilege tests: {0} passed, {1} failed; TEMP/mock only.' -f $script:Passed,$script:Failed))
if($script:Failed -gt 0){exit 1}
