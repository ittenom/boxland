$ErrorActionPreference = "Continue"
$proc = Start-Process docker -ArgumentList "ps" -PassThru -WindowStyle Hidden `
    -RedirectStandardOutput docker_ps.txt -RedirectStandardError docker_ps.err
$done = $proc.WaitForExit(15000)
if ($done) {
    Write-Host "EXITED with $($proc.ExitCode)"
    Get-Content docker_ps.txt -ErrorAction SilentlyContinue
} else {
    Write-Host "STILL RUNNING after 15s; killing"
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
}
Remove-Item docker_ps.txt, docker_ps.err -ErrorAction SilentlyContinue
