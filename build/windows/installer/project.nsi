Unicode true

# This installer is intentionally a single package. The user chooses the
# installation scope at runtime instead of downloading separate machine/user
# installers.

!define REQUEST_EXECUTION_LEVEL "highest"
!define PRODUCT_EXECUTABLE "MaxKB 本地文件同步工具.exe"
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
!define MUI_ABORTWARNING

Var InstallScope
Var DetectedInstallScope
Var ScopeUserRadio
Var ScopeMachineRadio

Function .onInit
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
        # A standard user cannot write Program Files. Tell them to select the
        # per-user option instead of allowing a later, opaque access failure.
        UserInfo::GetAccountType
        Pop $1
        ${If} $1 != "Admin"
        ${AndIf} $1 != "Power"
            MessageBox MB_ICONEXCLAMATION|MB_OK "当前账户没有为所有用户安装所需的管理员权限，请选择“仅为当前用户安装”。"
            Abort
        ${EndIf}
        ${If} $DetectedInstallScope != "machine"
            StrCpy $INSTDIR "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
        ${EndIf}
    ${Else}
        StrCpy $InstallScope "user"
        ${If} $DetectedInstallScope != "user"
            StrCpy $INSTDIR "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
        ${EndIf}
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
InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
ShowInstDetails show

Section "install"
    ${If} $InstallScope == "machine"
        SetShellVarContext all
    ${Else}
        SetShellVarContext current
    ${EndIf}

    !insertmacro wails.webview2runtime
    SetOutPath $INSTDIR
    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortcut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    WriteUninstaller "$INSTDIR\uninstall.exe"
    SetRegView 64
    ${If} $InstallScope == "machine"
        WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
        WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
        WriteRegStr HKLM "${UNINST_KEY}" "InstallScope" "machine"
        WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
        WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
        ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
        IntFmt $0 "0x%08X"
        WriteRegDWORD HKLM "${UNINST_KEY}" "EstimatedSize" "$0"
    ${Else}
        WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
        WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
        WriteRegStr HKCU "${UNINST_KEY}" "InstallScope" "user"
        WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
        WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
        ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
        IntFmt $0 "0x%08X"
        WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
    ${EndIf}
SectionEnd

Section "uninstall"
    # Remove shortcuts from both shell contexts. This keeps uninstall reliable
    # even when the installer was launched from an elevated account.
    SetShellVarContext current
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    SetShellVarContext all
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    # The application data directory is intentionally outside $INSTDIR and
    # must survive uninstall so tasks, logs, snapshots and credentials remain
    # available for a later reinstall. Only remove installed program files.
    RMDir /r "$INSTDIR"
    DeleteRegKey HKCU "${UNINST_KEY}"
    DeleteRegKey HKLM "${UNINST_KEY}"
SectionEnd
