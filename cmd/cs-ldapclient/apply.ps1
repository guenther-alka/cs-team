# cs-ldapclient: writes the pGina settings (LDAP + LocalMachine plugins) through pGina's own settings API.
# Called by cs-ldapclient.exe with a JSON file of the settings. Needs administrator rights.
param([Parameter(Mandatory=$true)][string]$Json)
$ErrorActionPreference = 'Stop'
$c = Get-Content -Raw -Encoding UTF8 $Json | ConvertFrom-Json
$d = 'C:\Program Files\pGina.fork'
$ldap = '0f52390b-c781-43ae-bd62-553c77fa4cf7'
$lm   = '12fa152d-a2e3-4c8d-9535-5dcd49dfcb6d'

function Resolve-LocalGroup([string]$n) {
    $sid = $null
    switch -Regex ($n) {
        '^(Users|Benutzer)$'                    { $sid = 'S-1-5-32-545' }
        '^(Administrators|Administratoren)$'    { $sid = 'S-1-5-32-544' }
        '^(Guests|G.ste)$'                      { $sid = 'S-1-5-32-546' }
        '^(Remote Desktop Users|Remotedesktopbenutzer)$' { $sid = 'S-1-5-32-555' }
    }
    if ($sid) { return (New-Object Security.Principal.SecurityIdentifier $sid).Translate([Security.Principal.NTAccount]).Value.Split('\')[-1] }
    return $n
}

$rules = @()
foreach ($g in @($c.Rules)) {
    if (-not $g.Group) { continue }
    $local = Resolve-LocalGroup $g.Local
    if (-not (Get-LocalGroup -Name $local -ErrorAction SilentlyContinue)) { Write-Warning "local group '$local' does not exist yet: create it, pGina only adds users to existing groups" }
    $rules += ("0`t0`tcn=" + $g.Group + ",ou=groups," + $c.Base + "`t(memberUid=%u)`t" + $local)
}

$k = 'HKLM:\SOFTWARE\pGina3.fork'
if (-not (Test-Path $k)) { $null = New-Item $k -Force }   # only when missing: -Force on an existing key wipes it
Stop-Service pgina -WarningAction SilentlyContinue -ErrorAction SilentlyContinue
foreach ($n in 'log4net','Abstractions','pGina.Shared','pGina.Core') { $null = [Reflection.Assembly]::LoadFrom("$d\$n.dll") }

$src = @'
using System;
public class CsLdapApply {
  public static void Run(string host, int port, string dnPattern, string groupDn, string cert,
                         string searchDn, string searchPw, string[] rules, bool removeProfiles) {
    dynamic p = new pGina.Shared.Settings.pGinaDynamicSettings(new Guid("0f52390b-c781-43ae-bd62-553c77fa4cf7"));
    p.LdapHost = new string[] { host };
    p.LdapPort = port;
    p.UseSsl = true;
    p.UseTls = false;
    p.RequireCert = true;
    p.ServerCertFile = cert;
    p.DnPattern = dnPattern;
    p.DoSearch = false;
    p.GroupDnPattern = groupDn;
    p.GroupMemberAttrib = "memberUid";
    p.AuthzRequireAuth = true;
    p.AuthzDefault = true;
    p.UseAuthBindForAuthzAndGateway = false;
    p.SearchDN = searchDn;
    p.SetEncryptedSetting("SearchPW", searchPw);
    p.GroupGatewayRules = rules;
    dynamic m = new pGina.Shared.Settings.pGinaDynamicSettings(new Guid("12fa152d-a2e3-4c8d-9535-5dcd49dfcb6d"));
    m.RemoveProfiles = removeProfiles;
    m.ScramblePasswords = removeProfiles;
    dynamic r = new pGina.Shared.Settings.pGinaDynamicSettings();
    string[] order = new string[] { "0f52390b-c781-43ae-bd62-553c77fa4cf7", "12fa152d-a2e3-4c8d-9535-5dcd49dfcb6d" };
    r.IPluginAuthentication_Order = order;
    r.IPluginAuthenticationGateway_Order = order;
  }
}
'@
Add-Type -TypeDefinition $src -ReferencedAssemblies "$d\pGina.Shared.dll","$d\Abstractions.dll","Microsoft.CSharp","System.Core"
[CsLdapApply]::Run([string]$c.Server, [int]$c.Port, "uid=%u,ou=people,$($c.Base)", "cn=%g,ou=groups,$($c.Base)",
                   [string]$c.CertPath, [string]$c.ReaderDN, [string]$c.ReaderPassword, [string[]]$rules, [bool]$c.RemoveProfiles)

# which plugin does what: UI=1 Authentication=2 Authorization=4 Gateway=8 Notification=16
$k = 'HKLM:\SOFTWARE\pGina3.fork'
New-ItemProperty $k -Name $ldap -Value 15 -PropertyType DWord -Force | Out-Null
New-ItemProperty $k -Name $lm   -Value 27 -PropertyType DWord -Force | Out-Null
Start-Service pgina
'pGina settings written'
