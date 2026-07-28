#Requires -Version 5.1
<#
.SYNOPSIS
  Creates or updates an Entra ID app registration for M365 Backup.

.DESCRIPTION
  - Creates a single-tenant app registration (or updates an existing one)
  - Adds Microsoft Graph Application permissions
  - Grants admin consent (app role assignments on the service principal)
  - Creates a client secret and prints Tenant ID, Client ID, Secret, Expiry

  Run as Global Administrator (or Application Administrator + Privileged Role
  Administrator) in the customer Microsoft 365 tenant.

.PARAMETER DisplayName
  App display name. Default: M365 Backup

.PARAMETER RedirectUri
  Web redirect URI for the admin-consent callback, e.g.
  https://backup.example.com/api/consent/callback
  Optional but recommended so the UI consent button works.

.PARAMETER AppId
  Existing Application (client) ID. If set, updates that app instead of creating one.

.PARAMETER SecretValidMonths
  Client secret lifetime in months (1-24). Default: 24

.PARAMETER NewSecret
  When updating an existing app, always create a new client secret.

.PARAMETER SkipOptional
  Skip optional permissions (Mail.ReadWrite, Files.ReadWrite.All,
  Calendars.Read, Contacts.Read).

.PARAMETER TenantId
  Directory (tenant) ID to connect to. Default: common (picker).

.EXAMPLE
  # New app in the signed-in tenant
  .\Register-M365BackupApp.ps1 -RedirectUri "https://backup.example.com/api/consent/callback"

.EXAMPLE
  # Fix an existing app (add missing permissions + consent, keep secret)
  .\Register-M365BackupApp.ps1 -AppId "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"

.EXAMPLE
  # Existing app + rotate secret
  .\Register-M365BackupApp.ps1 -AppId "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" -NewSecret
#>
[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [string] $DisplayName = "M365 Backup",

    [string] $RedirectUri = "",

    [string] $AppId = "",

    [ValidateRange(1, 24)]
    [int] $SecretValidMonths = 24,

    [switch] $NewSecret,

    [switch] $SkipOptional,

    [string] $TenantId = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$GraphAppId = "00000003-0000-0000-c000-000000000000"

$RequiredPermissions = @(
    "User.Read.All",
    "Mail.Read",
    "Files.Read.All",
    "Sites.Read.All",
    "Team.ReadBasic.All",
    "ChannelMessage.Read.All",
    "Chat.Read.All",
    "Application.Read.All"
)

$OptionalPermissions = @(
    "Mail.ReadWrite",
    "Files.ReadWrite.All",
    "Calendars.Read",
    "Contacts.Read"
)

function Ensure-GraphModules {
    $modules = @(
        "Microsoft.Graph.Authentication",
        "Microsoft.Graph.Applications"
    )
    foreach ($name in $modules) {
        if (-not (Get-Module -ListAvailable -Name $name)) {
            Write-Host "Installing module $name (CurrentUser)..." -ForegroundColor Cyan
            Install-Module -Name $name -Scope CurrentUser -Force -AllowClobber
        }
        Import-Module $name -ErrorAction Stop
    }
}

function Get-GraphAppRoleMap {
    param([Parameter(Mandatory)] $GraphServicePrincipal)

    $map = @{}
    foreach ($role in $GraphServicePrincipal.AppRoles) {
        if ($role.Value -and ($role.AllowedMemberTypes -contains "Application")) {
            $map[$role.Value] = $role.Id
        }
    }
    return $map
}

function Ensure-AppRoleAssignments {
    param(
        [Parameter(Mandatory)] [string] $ServicePrincipalId,
        [Parameter(Mandatory)] [string] $ResourceId,
        [Parameter(Mandatory)] [hashtable] $RoleMap,
        [Parameter(Mandatory)] [string[]] $PermissionValues
    )

    $existing = @(Get-MgServicePrincipalAppRoleAssignment -ServicePrincipalId $ServicePrincipalId -All -ErrorAction SilentlyContinue)
    $existingRoleIds = @{}
    foreach ($a in $existing) {
        if ($a.ResourceId -eq $ResourceId) {
            $existingRoleIds[$a.AppRoleId.ToString()] = $true
        }
    }

    $added = @()
    $skipped = @()
    foreach ($perm in $PermissionValues) {
        if (-not $RoleMap.ContainsKey($perm)) {
            Write-Warning "Graph app role not found: $perm (skipped)"
            continue
        }
        $roleId = $RoleMap[$perm].ToString()
        if ($existingRoleIds.ContainsKey($roleId)) {
            $skipped += $perm
            continue
        }
        if ($PSCmdlet.ShouldProcess($perm, "Grant application permission (admin consent)")) {
            New-MgServicePrincipalAppRoleAssignment `
                -ServicePrincipalId $ServicePrincipalId `
                -PrincipalId $ServicePrincipalId `
                -ResourceId $ResourceId `
                -AppRoleId $RoleMap[$perm] | Out-Null
            $added += $perm
        }
    }
    return [pscustomobject]@{ Added = $added; AlreadyGranted = $skipped }
}

Ensure-GraphModules

$wanted = [System.Collections.Generic.List[string]]::new()
foreach ($p in $RequiredPermissions) { [void]$wanted.Add($p) }
if (-not $SkipOptional) {
    foreach ($p in $OptionalPermissions) { [void]$wanted.Add($p) }
}

$scopes = @(
    "Application.ReadWrite.All",
    "AppRoleAssignment.ReadWrite.All",
    "Directory.Read.All"
)

Write-Host "Connecting to Microsoft Graph (interactive)..." -ForegroundColor Cyan
$connectParams = @{
    Scopes = $scopes
    NoWelcome = $true
}
if ($TenantId) {
    $connectParams["TenantId"] = $TenantId
}
Connect-MgGraph @connectParams | Out-Null

$ctx = Get-MgContext
if (-not $ctx) {
    throw "Not connected to Microsoft Graph."
}
$resolvedTenantId = $ctx.TenantId
Write-Host "Signed in as $($ctx.Account) - tenant $resolvedTenantId" -ForegroundColor Green

$graphSp = @(Get-MgServicePrincipal -Filter "appId eq '$GraphAppId'") | Select-Object -First 1
if (-not $graphSp) {
    throw "Microsoft Graph service principal not found in this tenant."
}
$roleMap = Get-GraphAppRoleMap -GraphServicePrincipal $graphSp

$app = $null
$createdApp = $false
if ($AppId) {
    $app = @(Get-MgApplication -Filter "appId eq '$AppId'") | Select-Object -First 1
    if (-not $app) {
        throw "Application with AppId '$AppId' not found in this tenant."
    }
    Write-Host "Updating existing app: $($app.DisplayName) ($($app.AppId))" -ForegroundColor Cyan
} else {
    if ($PSCmdlet.ShouldProcess($DisplayName, "Create application registration")) {
        $web = @{
            ImplicitGrantSettings = @{
                EnableAccessTokenIssuance = $false
                EnableIdTokenIssuance     = $false
            }
        }
        if ($RedirectUri) {
            $web["RedirectUris"] = @($RedirectUri)
        }
        $app = New-MgApplication `
            -DisplayName $DisplayName `
            -SignInAudience "AzureADMyOrg" `
            -Web $web
        $createdApp = $true
        Write-Host "Created app: $($app.DisplayName) ($($app.AppId))" -ForegroundColor Green
    }
}

if (-not $app) {
    throw "No application object available."
}

# Ensure redirect URI on existing / just-created apps
if ($RedirectUri) {
    $uris = @()
    if ($app.Web -and $app.Web.RedirectUris) {
        $uris = @($app.Web.RedirectUris)
    }
    if ($uris -notcontains $RedirectUri) {
        $uris += $RedirectUri
        if ($PSCmdlet.ShouldProcess($RedirectUri, "Add web redirect URI")) {
            Update-MgApplication -ApplicationId $app.Id -Web @{ RedirectUris = $uris }
            Write-Host "Added redirect URI: $RedirectUri" -ForegroundColor Green
        }
    }
}

# Required resource access on the application object (portal listing)
$requiredAccess = @()
$resourceAccess = @()
foreach ($perm in $wanted) {
    if (-not $roleMap.ContainsKey($perm)) { continue }
    $resourceAccess += @{
        Id   = $roleMap[$perm]
        Type = "Role"
    }
}
$requiredAccess += @{
    ResourceAppId  = $GraphAppId
    ResourceAccess = $resourceAccess
}
if ($PSCmdlet.ShouldProcess($app.AppId, "Set requiredResourceAccess on application")) {
    Update-MgApplication -ApplicationId $app.Id -RequiredResourceAccess $requiredAccess
}

# Service principal (enterprise app)
$sp = @(Get-MgServicePrincipal -Filter "appId eq '$($app.AppId)'") | Select-Object -First 1
if (-not $sp) {
    if ($PSCmdlet.ShouldProcess($app.AppId, "Create service principal")) {
        $sp = New-MgServicePrincipal -AppId $app.AppId -DisplayName $app.DisplayName
        Write-Host "Created service principal: $($sp.Id)" -ForegroundColor Green
    }
}
if (-not $sp) {
    throw "Service principal missing for app $($app.AppId)."
}

$consent = Ensure-AppRoleAssignments `
    -ServicePrincipalId $sp.Id `
    -ResourceId $graphSp.Id `
    -RoleMap $roleMap `
    -PermissionValues @($wanted)

if ($consent.Added.Count -gt 0) {
    Write-Host ("Granted permissions: " + ($consent.Added -join ", ")) -ForegroundColor Green
}
if ($consent.AlreadyGranted.Count -gt 0) {
    Write-Host ("Already consented: " + ($consent.AlreadyGranted -join ", ")) -ForegroundColor DarkGray
}

# Client secret
$secretValue = $null
$secretExpires = $null
$needSecret = $createdApp -or $NewSecret
if ($needSecret) {
    $end = (Get-Date).ToUniversalTime().AddMonths($SecretValidMonths)
    if ($PSCmdlet.ShouldProcess($app.AppId, "Create client secret (valid $SecretValidMonths months)")) {
        $cred = Add-MgApplicationPassword -ApplicationId $app.Id -PasswordCredential @{
            DisplayName = "m365backup-$(Get-Date -Format 'yyyyMMdd')"
            EndDateTime = $end
        }
        $secretValue = $cred.SecretText
        $secretExpires = $cred.EndDateTime
        Write-Host "Client secret created (shown once below)." -ForegroundColor Green
    }
}

Write-Host ""
Write-Host "========== M365 Backup - Azure app ==========" -ForegroundColor Yellow
Write-Host ("Azure Tenant ID : {0}" -f $resolvedTenantId)
Write-Host ("Application ID  : {0}" -f $app.AppId)
if ($secretValue) {
    Write-Host ("Client secret   : {0}" -f $secretValue) -ForegroundColor Magenta
    Write-Host ("Secret expires  : {0:yyyy-MM-dd}" -f ([datetime]$secretExpires))
} else {
    Write-Host "Client secret   : (unchanged - pass -NewSecret to rotate)" -ForegroundColor DarkGray
}
if ($RedirectUri) {
    Write-Host ("Redirect URI    : {0}" -f $RedirectUri)
}
Write-Host "================================================" -ForegroundColor Yellow
Write-Host ""
Write-Host "Paste Tenant ID, Application ID, Client secret (and expiry) into M365 Backup -> New tenant." -ForegroundColor Cyan
if (-not $secretValue -and -not $createdApp) {
    Write-Host "No new secret was created. Use the existing secret in the backup UI, or re-run with -NewSecret." -ForegroundColor Cyan
}
Write-Host "Store the secret offline - it is not shown again." -ForegroundColor Cyan

Disconnect-MgGraph | Out-Null
