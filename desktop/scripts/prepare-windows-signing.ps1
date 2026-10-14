$ErrorActionPreference = "Stop"

"EVSIGN_REQUIRE=$env:REQUIRE_WINDOWS_SIGNING" | Out-File $env:GITHUB_ENV -Encoding utf8 -Append
if ($env:REQUIRE_WINDOWS_SIGNING -ne "true") {
    Write-Host "Windows signing is not required for this dry-run; building unsigned."
    exit 0
}
if ([string]::IsNullOrWhiteSpace($env:EVSIGN_KEY)) {
    throw "Missing EVSIGN_KEY secret required for Windows release signing."
}
Write-Output "::add-mask::$env:EVSIGN_KEY"

$client = Join-Path $env:RUNNER_TEMP "evsign-client.exe"
Invoke-WebRequest -Uri "https://mc.evsign.cn/evsign-client-cli-windows-latest" `
    -OutFile $client -MaximumRedirection 5 -TimeoutSec 120
Write-Host "EVSign CLI SHA256: $((Get-FileHash -LiteralPath $client -Algorithm SHA256).Hash)"
$signature = Get-AuthenticodeSignature -LiteralPath $client
if ($signature.Status -ne "Valid" -or $null -eq $signature.SignerCertificate -or
    $signature.SignerCertificate.Subject -notlike "*Keroro Software Ltd*") {
    throw "EVSign CLI must have a valid Authenticode signature from Keroro Software Ltd."
}
"EVSIGN_CLIENT=$client" | Out-File $env:GITHUB_ENV -Encoding utf8 -Append
