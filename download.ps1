$BaseUrl = "http://ckptcli.smartapi.ru/binary"
$OutputName = "scaner.exe"

$ProcArch = $env:PROCESSOR_ARCHITECTURE
$Arch = switch -Regex ($ProcArch) {
    "ARM64"  { "arm64" }
    "AMD64"  { "amd64" }
    "x86"    { "amd64" }  # 32-bit — качаем amd64, если нет отдельного бинарника
    default  { "amd64" }
}

$FileName = "scaner-windows-$Arch.exe"
$Url = "$BaseUrl/$FileName"

Write-Host "Архитектура: $Arch, загрузка: $Url"
try {
    Invoke-WebRequest -Uri $Url -OutFile $OutputName -UseBasicParsing
    Write-Host "Сохранено: $OutputName"
} catch {
    Write-Error "Ошибка загрузки: $_"
}
