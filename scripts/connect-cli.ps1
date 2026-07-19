#!/usr/bin/env pwsh
<#
.SYNOPSIS
    One-click setup to connect common AI CLI tools to a CatieAPI gateway.

.DESCRIPTION
    Sets the ANTHROPIC_* and OPENAI_* environment variables used by Claude Code,
    Aider, and any OpenAI SDK based tool, and creates a Codex CLI provider entry
    when one is not present yet. Environment variables are written at User scope
    so new terminals pick them up, and also applied to the current session.

.PARAMETER BaseUrl
    CatieAPI base URL. Defaults to https://shiliyuming.com. The /v1 suffix is
    optional; the gateway accepts both.

.PARAMETER ApiKey
    CatieAPI key (cat_...). Prompted for when omitted.

.PARAMETER Model
    Default model id or alias to use, e.g. ds or gpt-5.5.

.PARAMETER SmallModel
    Fast/background model for Claude Code. Defaults to -Model.

.PARAMETER Scope
    Environment variable scope: User (default) or Process (current shell only).

.EXAMPLE
    ./scripts/connect-cli.ps1 -ApiKey "cat_xxx" -Model "ds"
#>
[CmdletBinding()]
param(
    [string]$BaseUrl = "https://shiliyuming.com",
    [string]$ApiKey,
    [string]$Model = "ds",
    [string]$SmallModel,
    [ValidateSet("User", "Process")]
    [string]$Scope = "User"
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($ApiKey)) {
    $ApiKey = Read-Host "Enter your CatieAPI key (cat_...)"
}
if ([string]::IsNullOrWhiteSpace($ApiKey)) {
    Write-Error "An API key is required."
    exit 1
}
if ([string]::IsNullOrWhiteSpace($SmallModel)) {
    $SmallModel = $Model
}

$BaseUrl = $BaseUrl.TrimEnd("/")
$V1BaseUrl = if ($BaseUrl -match "/v1$") { $BaseUrl } else { "$BaseUrl/v1" }

$vars = [ordered]@{
    "ANTHROPIC_BASE_URL"        = $BaseUrl
    "ANTHROPIC_AUTH_TOKEN"      = $ApiKey
    "ANTHROPIC_MODEL"           = $Model
    "ANTHROPIC_SMALL_FAST_MODEL" = $SmallModel
    "OPENAI_BASE_URL"           = $BaseUrl
    "OPENAI_API_BASE"           = $BaseUrl
    "OPENAI_API_KEY"            = $ApiKey
    "CATIEAPI_BASE_URL"         = $BaseUrl
    "CATIEAPI_KEY"              = $ApiKey
}

Write-Host ""
Write-Host "Connecting CLI tools to CatieAPI" -ForegroundColor Cyan
Write-Host "  Base URL : $BaseUrl"
Write-Host "  Model    : $Model (fast: $SmallModel)"
Write-Host "  Scope    : $Scope"
Write-Host ""

foreach ($name in $vars.Keys) {
    $value = $vars[$name]
    Set-Item -Path "env:$name" -Value $value
    if ($Scope -eq "User") {
        [Environment]::SetEnvironmentVariable($name, $value, "User")
    }
    $shown = if ($name -match "KEY|TOKEN") { $value.Substring(0, [Math]::Min(8, $value.Length)) + "..." } else { $value }
    Write-Host ("  set {0,-26} = {1}" -f $name, $shown) -ForegroundColor DarkGray
}

# Codex CLI: create a provider config only when the user has none yet, so an
# existing config is never overwritten.
$codexDir = Join-Path $HOME ".codex"
$codexConfig = Join-Path $codexDir "config.toml"
$codexBlock = @"
model = "$Model"
model_provider = "catieapi"

[model_providers.catieapi]
name = "CatieAPI"
base_url = "$V1BaseUrl"
env_key = "CATIEAPI_KEY"
wire_api = "chat"
"@

Write-Host ""
if (Test-Path $codexConfig) {
    Write-Host "Codex CLI config already exists at $codexConfig" -ForegroundColor Yellow
    Write-Host "Add this provider block manually if needed:" -ForegroundColor Yellow
    Write-Host $codexBlock -ForegroundColor DarkGray
}
else {
    New-Item -ItemType Directory -Force -Path $codexDir | Out-Null
    Set-Content -Path $codexConfig -Value $codexBlock -Encoding UTF8
    Write-Host "Wrote Codex CLI config to $codexConfig" -ForegroundColor Green
}

Write-Host ""
Write-Host "Done." -ForegroundColor Green
Write-Host "Open a new terminal so User-scope variables take effect, then:"
Write-Host "  Claude Code : claude"
Write-Host "  Codex CLI   : codex"
Write-Host "  Aider       : aider --model openai/$Model"
Write-Host ""
if ($Scope -eq "User") {
    Write-Host "To undo, clear the variables, e.g.:" -ForegroundColor DarkGray
    Write-Host "  [Environment]::SetEnvironmentVariable('ANTHROPIC_AUTH_TOKEN', `$null, 'User')" -ForegroundColor DarkGray
}
