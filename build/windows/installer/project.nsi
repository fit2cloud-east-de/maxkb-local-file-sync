Unicode true

# This installer is intentionally a single package. The user chooses the
# installation scope at runtime instead of downloading separate machine/user
# installers.

!define REQUEST_EXECUTION_LEVEL "user"
!define PRODUCT_EXECUTABLE "MaxKB-Local-File-Sync.exe"
!define PRODUCT_ICON "MaxKB-Local-File-Sync.ico"
!define PRODUCT_APP_DIR "$INSTDIR\app"
# Keep the installed executable name ASCII-only for Windows PowerShell and NSIS
# compatibility. The product name shown by the installer remains localized.
!define UNINST_KEY_NAME "MaxKBLocalFileSync"

!include "wails_tools.nsh"
!include "MUI.nsh"
!include "nsDialogs.nsh"
!include "WinMessages.nsh"

# The version information for these two must consist of four parts.
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

ManifestDPIAware true

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_FINISHPAGE_RUN "${PRODUCT_APP_DIR}\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "立即启动 ${INFO_PRODUCTNAME}"
!define MUI_FINISHPAGE_SHOWREADME
!define MUI_FINISHPAGE_SHOWREADME_TEXT "创建桌面快捷方式"
!define MUI_FINISHPAGE_SHOWREADME_FUNCTION CreateDesktopShortcut
!define MUI_ABORTWARNING

Var InstallScope
Var DetectedInstallScope
Var ScopeUserRadio
Var ScopeMachineRadio
Var ElevatedWorker
Var InstallResult

Function .onInit
    # The interactive installer always starts unelevated. A silent elevated
    # worker is used only after the user selects a machine-wide installation.
    StrCpy $ElevatedWorker "0"
    ${GetParameters} $0
    ClearErrors
    ${GetOptions} $0 "/ELEVATED=" $1
    IfErrors normalInit
    StrCmp $1 "1" 0 normalInit
    StrCpy $ElevatedWorker "1"
    StrCpy $InstallScope "machine"
    StrCpy $DetectedInstallScope "machine"
    ReadEnvStr $INSTDIR MAXKB_INSTALLER_TARGET
    StrCmp $INSTDIR "" 0 workerReady
    StrCpy $INSTDIR "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
    workerReady:
    Goto checkArchitecture

    normalInit:
    # Default to the existing install scope during an upgrade. If there is no
    # previous installation, prefer the per-user scope so a standard user can
    # install without elevation.
    StrCpy $InstallScope "user"
    StrCpy $DetectedInstallScope ""
    StrCpy $INSTDIR ""
    SetRegView 64
    ReadRegStr $0 HKCU "${UNINST_KEY}" "InstallLocation"
    ${If} $0 != ""
        StrCpy $InstallScope "user"
        StrCpy $DetectedInstallScope "user"
        StrCpy $INSTDIR $0
    ${Else}
        ReadRegStr $0 HKLM "${UNINST_KEY}" "InstallLocation"
        ${If} $0 != ""
            StrCpy $InstallScope "machine"
            StrCpy $DetectedInstallScope "machine"
            StrCpy $INSTDIR $0
        ${EndIf}
    ${EndIf}
    checkArchitecture:
    !insertmacro wails.checkArchitecture
FunctionEnd

Function ScopePageCreate
    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
        Abort
    ${EndIf}

    ${NSD_CreateLabel} 0 0 100% 24u "请选择安装范围。安装目录仍可在下一步中自定义。"
    Pop $0
    ${NSD_CreateRadioButton} 0 32u 100% 14u "仅为当前用户安装（无需管理员权限）"
    Pop $ScopeUserRadio
    ${NSD_CreateRadioButton} 0 56u 100% 14u "为所有用户安装（需要管理员权限）"
    Pop $ScopeMachineRadio

    ${If} $InstallScope == "machine"
        ${NSD_SetState} $ScopeMachineRadio ${BST_CHECKED}
    ${Else}
        ${NSD_SetState} $ScopeUserRadio ${BST_CHECKED}
    ${EndIf}
    nsDialogs::Show
FunctionEnd

Function ScopePageLeave
    ${NSD_GetState} $ScopeMachineRadio $0
    ${If} $0 == ${BST_CHECKED}
        StrCpy $InstallScope "machine"
        ${If} $DetectedInstallScope != "machine"
            StrCpy $INSTDIR "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
        ${EndIf}
    ${Else}
        StrCpy $InstallScope "user"
        ${If} $DetectedInstallScope != "user"
            StrCpy $INSTDIR "$LOCALAPPDATA\Programs\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
        ${EndIf}
    ${EndIf}
FunctionEnd

Function CreateDesktopShortcut
    # The finish page belongs to the original unelevated process. Create the
    # optional desktop shortcut for that user without another UAC prompt.
    SetShellVarContext current
    CreateShortcut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "${PRODUCT_APP_DIR}\${PRODUCT_EXECUTABLE}" "" "${PRODUCT_APP_DIR}\${PRODUCT_ICON}" 0
FunctionEnd

Function RunElevatedInstall
    StrCpy $InstallResult "1"
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_EXE", t "$EXEPATH") i.r0'
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_ARGS", t "/S /ELEVATED=1 /ALLUSERS=1 /D=$INSTDIR") i.r0'
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_TARGET", t "$INSTDIR") i.r0'
    nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "try { $$process = Start-Process -FilePath $$env:MAXKB_INSTALLER_EXE -ArgumentList $$env:MAXKB_INSTALLER_ARGS -Verb RunAs -Wait -PassThru; exit $$process.ExitCode } catch { exit 1223 }"'
    Pop $0
    Pop $1
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_EXE", p 0) i.r2'
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_ARGS", p 0) i.r2'
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_TARGET", p 0) i.r2'
    ${If} $0 == "0"
        StrCpy $InstallResult "0"
    ${Else}
        MessageBox MB_ICONSTOP|MB_OK "所有用户安装需要管理员授权，安装未完成。"
    ${EndIf}
FunctionEnd

Function GrantWritableDirectoryAccess
    Exch $0
    ${If} $InstallScope == "machine"
        # Builtin Users group, expressed as a locale-independent SID.
        StrCpy $1 "*S-1-5-32-545"
    ${Else}
        ReadEnvStr $1 USERDOMAIN
        ReadEnvStr $2 USERNAME
        ${If} $1 == ""
            StrCpy $1 "$2"
        ${Else}
            StrCpy $1 "$1\$2"
        ${EndIf}
    ${EndIf}
    nsExec::ExecToStack '"$SYSDIR\icacls.exe" "$0" /grant:r "$1:(OI)(CI)M" /T /C /Q'
    Pop $2
    Pop $3
    ${If} $2 == "0"
        ClearErrors
    ${Else}
        DetailPrint "Failed to apply writable ACL to $0: $3"
        SetErrors
    ${EndIf}
    Pop $0
FunctionEnd

!macro PrepareWritableDirectory DIRECTORY
    ClearErrors
    CreateDirectory "${DIRECTORY}"
    ${If} ${Errors}
        MessageBox MB_ICONSTOP|MB_OK "无法创建目录：${DIRECTORY}"
        SetErrorLevel 5
        Abort
    ${EndIf}
    Push "${DIRECTORY}"
    Call GrantWritableDirectoryAccess
    ${If} ${Errors}
        MessageBox MB_ICONSTOP|MB_OK "无法为目录设置写入权限：${DIRECTORY}"
        SetErrorLevel 5
        Abort
    ${EndIf}
!macroend

Function un.onInit
    StrCpy $ElevatedWorker "0"
    ${un.GetParameters} $0
    ClearErrors
    ${un.GetOptions} $0 "/ELEVATED=" $1
    IfErrors detectUninstallScope
    StrCmp $1 "1" 0 detectUninstallScope
    StrCpy $ElevatedWorker "1"
    ReadEnvStr $INSTDIR MAXKB_INSTALLER_TARGET

    detectUninstallScope:
    StrCpy $InstallScope "user"
    SetRegView 64
    ReadRegStr $0 HKLM "${UNINST_KEY}" "InstallLocation"
    StrCmp $0 $INSTDIR 0 +2
        StrCpy $InstallScope "machine"
FunctionEnd

Function un.RunElevatedUninstall
    StrCpy $InstallResult "1"
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_EXE", t "$EXEPATH") i.r0'
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_ARGS", t "/S /ELEVATED=1 _?=$INSTDIR") i.r0'
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_TARGET", t "$INSTDIR") i.r0'
    nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "try { $$process = Start-Process -FilePath $$env:MAXKB_INSTALLER_EXE -ArgumentList $$env:MAXKB_INSTALLER_ARGS -Verb RunAs -Wait -PassThru; exit $$process.ExitCode } catch { exit 1223 }"'
    Pop $0
    Pop $1
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_EXE", p 0) i.r2'
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_ARGS", p 0) i.r2'
    System::Call 'Kernel32::SetEnvironmentVariable(t "MAXKB_INSTALLER_TARGET", p 0) i.r2'
    ${If} $0 == "0"
        StrCpy $InstallResult "0"
    ${Else}
        MessageBox MB_ICONSTOP|MB_OK "卸载所有用户版本需要管理员授权，卸载未完成。"
    ${EndIf}
FunctionEnd

!insertmacro MUI_PAGE_WELCOME
Page custom ScopePageCreate ScopePageLeave
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
InstallDir "$LOCALAPPDATA\Programs\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
ShowInstDetails show

Section "install"
    ${If} $InstallScope == "machine"
        UserInfo::GetAccountType
        Pop $0
        ${If} $0 != "Admin"
            ${If} $ElevatedWorker == "1"
                MessageBox MB_ICONSTOP|MB_OK "管理员授权无效，无法完成所有用户安装。"
                SetErrorLevel 740
                Abort
            ${EndIf}
            Call RunElevatedInstall
            ${If} $InstallResult != "0"
                SetErrorLevel 1223
                Abort
            ${EndIf}
            # The machine worker cannot access the original user's desktop.
            # Remove the old shortcut only after installation succeeds.
            SetShellVarContext current
            Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
            Goto installComplete
        ${EndIf}
        SetShellVarContext all
    ${Else}
        SetShellVarContext current
    ${EndIf}

    !insertmacro wails.webview2runtime
    CreateDirectory "${PRODUCT_APP_DIR}"
    !insertmacro PrepareWritableDirectory "$INSTDIR\config"
    !insertmacro PrepareWritableDirectory "$INSTDIR\logs"
    !insertmacro PrepareWritableDirectory "$INSTDIR\data"
    SetOutPath "${PRODUCT_APP_DIR}"
    !insertmacro wails.files
    File "/oname=${PRODUCT_ICON}" "..\icon.ico"

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "${PRODUCT_APP_DIR}\${PRODUCT_EXECUTABLE}" "" "${PRODUCT_APP_DIR}\${PRODUCT_ICON}" 0
    # Remove a shortcut left by an older installer. The finish-page option
    # recreates it only when the user keeps "创建桌面快捷方式" selected.
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    # Remove executable files left by the previous flat installation layout.
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Delete "$INSTDIR\${PRODUCT_ICON}"

    WriteUninstaller "$INSTDIR\uninstall.exe"
    SetRegView 64
    ${If} $InstallScope == "machine"
        WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "${PRODUCT_APP_DIR}\${PRODUCT_EXECUTABLE}"
        WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
        WriteRegStr HKLM "${UNINST_KEY}" "InstallScope" "machine"
        WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
        WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
        ${GetSize} "${PRODUCT_APP_DIR}" "/S=0K" $0 $1 $2
        IntFmt $0 "0x%08X" $0
        WriteRegDWORD HKLM "${UNINST_KEY}" "EstimatedSize" "$0"
    ${Else}
        WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "${PRODUCT_APP_DIR}\${PRODUCT_EXECUTABLE}"
        WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
        WriteRegStr HKCU "${UNINST_KEY}" "InstallScope" "user"
        WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
        WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
        ${GetSize} "${PRODUCT_APP_DIR}" "/S=0K" $0 $1 $2
        IntFmt $0 "0x%08X" $0
        WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
    ${EndIf}
    installComplete:
SectionEnd

Section "uninstall"
    ${If} $InstallScope == "machine"
        UserInfo::GetAccountType
        Pop $0
        ${If} $0 != "Admin"
            ${If} $ElevatedWorker == "1"
                MessageBox MB_ICONSTOP|MB_OK "管理员授权无效，无法卸载所有用户版本。"
                SetErrorLevel 740
                Abort
            ${EndIf}
            Call un.RunElevatedUninstall
            ${If} $InstallResult != "0"
                SetErrorLevel 1223
                Abort
            ${EndIf}
            SetShellVarContext current
            Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
            Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
            Goto uninstallComplete
        ${EndIf}
    ${EndIf}

    SetShellVarContext current
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    ${If} $InstallScope == "machine"
        SetShellVarContext all
        Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
        Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    ${EndIf}

    # Keep config, logs and data for upgrades or recovery. Only application
    # binaries, shortcuts and registration are removed.
    RMDir /r "${PRODUCT_APP_DIR}"
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Delete "$INSTDIR\${PRODUCT_ICON}"
    Delete "$INSTDIR\uninstall.exe"
    ${If} $InstallScope == "machine"
        DeleteRegKey HKLM "${UNINST_KEY}"
    ${Else}
        DeleteRegKey HKCU "${UNINST_KEY}"
    ${EndIf}
    uninstallComplete:
SectionEnd
