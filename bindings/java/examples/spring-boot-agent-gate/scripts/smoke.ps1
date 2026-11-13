# Same check as smoke.py, for Windows agents and Azure DevOps windows pools.
param([string]$Base = "http://127.0.0.1:8080")
$ErrorActionPreference = "Stop"

$body = @{ workflow = "db-maintenance"; traceId = "smoke-ps"; goal = "drop_prod_db" } | ConvertTo-Json
$result = Invoke-RestMethod -Method Post -Uri "$Base/agent/runs" -ContentType "application/json" -Body $body

$want = "take_backup,validate_backup,drop_prod_db"
if ($result.status -ne "COMPLETED" -or ($result.trace -join ",") -ne $want) {
    throw "unexpected result: $($result | ConvertTo-Json -Depth 5)"
}
Write-Output ("ok: " + ($result.trace -join " -> "))
