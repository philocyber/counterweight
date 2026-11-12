# Windows PowerShell 5.1: create an empty private credential file, or protect an
# existing regular file without changing its bytes or its parent directory.
param([Parameter(Mandatory = $true)][string]$Path, [string]$Template = '')
$ErrorActionPreference = 'Stop'
try {
    $target = [System.IO.Path]::GetFullPath($Path)
    $sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $security = New-Object System.Security.AccessControl.FileSecurity
    $security.SetSecurityDescriptorSddlForm("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;$sid)",
        [System.Security.AccessControl.AccessControlSections]::Access)
    if (Test-Path -LiteralPath $target) {
        $item = Get-Item -LiteralPath $target -Force
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.PSIsContainer) {
            throw 'Credential destination must be a regular file'
        }
        [System.IO.File]::SetAccessControl($target, $security)
    } else {
        $bytes = if ($Template) { [System.IO.File]::ReadAllBytes([System.IO.Path]::GetFullPath($Template)) } else { $null }
        # The descriptor is applied by CreateNew before any bytes are written.
        # No overwrite fallback: a concurrent creator must be inspected again.
        $file = New-Object System.IO.FileStream($target, [System.IO.FileMode]::CreateNew,
            [System.Security.AccessControl.FileSystemRights]::Write,
            [System.IO.FileShare]::None, 4096, [System.IO.FileOptions]::None, $security)
        try {
            if ($null -ne $bytes) { $file.Write($bytes, 0, $bytes.Length) }
            $file.Flush($true)
        } finally { $file.Dispose() }
    }
} catch {
    [Console]::Error.WriteLine('Unable to create or protect the credential file; no credential write should follow.')
    exit 1
}
