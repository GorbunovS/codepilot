; NSIS-скрипт установщика CodePilot для Windows.
; Сборка на маке: brew install nsis && makensis scripts/codepilot.nsi
; Требует предварительно собранной папки CodePilot-Windows/ (scripts/build_desktop.py).
; Путь к папке можно переопределить: makensis /DINST_SOURCE=/abs/path scripts/codepilot.nsi

!ifndef INST_SOURCE
  !define INST_SOURCE "CodePilot-Windows"
!endif
!ifndef OUTFILE
  !define OUTFILE "CodePilot-Setup.exe"
!endif

!define APP_NAME "CodePilot"
!define APP_VERSION "0.2.0"
!define PUBLISHER "CodePilot"

Name "${APP_NAME} ${APP_VERSION}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\${APP_NAME}"
InstallDirRegKey HKCU "Software\${APP_NAME}" "InstallDir"
RequestExecutionLevel admin

Page directory
Page instfiles

Section "Install"
  SetOutPath "$INSTDIR"
  File /r "${INST_SOURCE}\"

  ; Ярлык в меню Пуск
  CreateDirectory "$SMPROGRAMS\${APP_NAME}"
  CreateShortcut "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk" "$INSTDIR\CodePilot.vbs" "" "$INSTDIR\CodePilot.vbs" 0

  ; Ярлык на рабочем столе
  CreateShortcut "$DESKTOP\${APP_NAME}.lnk" "$INSTDIR\CodePilot.vbs" "" "$INSTDIR\CodePilot.vbs" 0

  ; Регистрация установки
  WriteRegStr HKCU "Software\${APP_NAME}" "InstallDir" "$INSTDIR"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
SectionEnd

Section "Uninstall"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir /r "$INSTDIR"
  Delete "$DESKTOP\${APP_NAME}.lnk"
  RMDir /r "$SMPROGRAMS\${APP_NAME}"
  DeleteRegKey HKCU "Software\${APP_NAME}"
SectionEnd
