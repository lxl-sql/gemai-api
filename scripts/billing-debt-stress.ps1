<#
.SYNOPSIS
Runs the isolated PostgreSQL and Redis billing-debt stress test.

.EXAMPLE
.\scripts\billing-debt-stress.ps1 -Users 5000 -Concurrency 64 -Replays 2

The test reads SQL_DSN and REDIS_CONN_STRING from .env, refuses non-loopback
targets, creates a random PostgreSQL schema, and uses Redis DB 15 by default.
It never flushes Redis and removes only the keys it created.
#>
[CmdletBinding()]
param(
    [ValidateRange(1, 100000)]
    [int]$Users = 5000,

    [ValidateRange(1, 512)]
    [int]$Concurrency = 64,

    [ValidateRange(0, 10)]
    [int]$Replays = 2,

    [ValidateRange(0, 255)]
    [int]$RedisDb = 15,

    [switch]$KeepSchema
)

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$environmentNames = @(
    'BILLING_STRESS_RUN',
    'BILLING_STRESS_USERS',
    'BILLING_STRESS_CONCURRENCY',
    'BILLING_STRESS_REPLAYS',
    'BILLING_STRESS_REDIS_DB',
    'BILLING_STRESS_KEEP_SCHEMA'
)
$previousValues = @{}
foreach ($name in $environmentNames) {
    $previousValues[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}

try {
    $env:BILLING_STRESS_RUN = '1'
    $env:BILLING_STRESS_USERS = [string]$Users
    $env:BILLING_STRESS_CONCURRENCY = [string]$Concurrency
    $env:BILLING_STRESS_REPLAYS = [string]$Replays
    $env:BILLING_STRESS_REDIS_DB = [string]$RedisDb
    $env:BILLING_STRESS_KEEP_SCHEMA = if ($KeepSchema) { '1' } else { '0' }

    Push-Location $repoRoot
    try {
        & go test -tags=stress ./model -run '^TestBillingDebtStress$' -count=1 -timeout=30m -v
        if ($LASTEXITCODE -ne 0) {
            throw "billing debt stress test failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}
finally {
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $previousValues[$name], 'Process')
    }
}
