param([string]$SignedFile)

$ErrorActionPreference = "Stop"
$outDir = Join-Path $PSScriptRoot "../../dist/desktop"
$files = @(
    "penguin-remote-win-x64.exe",
    "win-unpacked/Penguin Remote.exe",
    "win-unpacked/resources/bin/penguin-remote.exe",
    "win-unpacked/resources/elevate.exe"
)
if ($SignedFile) { $files = @($SignedFile) }
foreach ($file in $files) {
    $target = if ($SignedFile) { $file } else { Join-Path $outDir $file }
    if (-not (Test-Path -LiteralPath $target -PathType Leaf)) { throw "Missing Windows build output: $file" }
    $signature = Get-AuthenticodeSignature -LiteralPath $target
    if (-not $SignedFile -and $signature.Status -eq "NotSigned" -and $env:REQUIRE_WINDOWS_SIGNING -ne "true") {
        Write-Host "$file is unsigned; allowed for this dry-run."
        continue
    }
    if ($signature.Status -ne "Valid" -or $null -eq $signature.SignerCertificate) {
        throw "$file signature is not valid: $($signature.Status) $($signature.StatusMessage)"
    }
    Write-Host "$file signer: $($signature.SignerCertificate.Subject)"
    if ($null -eq $signature.TimeStamperCertificate) {
        Write-Host "::warning title=Windows signing::$file has no timestamp certificate."
    }
}
if (-not $SignedFile -and $env:REQUIRE_WINDOWS_SIGNING -eq "true") {
    & node (Join-Path $PSScriptRoot "write-signing-report.mjs") win x64
    if ($LASTEXITCODE -ne 0) { throw "Could not write Windows signing verification report." }
}
