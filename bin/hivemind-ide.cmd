@echo off
setlocal
set "ROOT=%~dp0.."
set "EXE=%ROOT%\ide\build\bin\hivemind-ide.exe"
if not exist "%EXE%" (
  set "EXE=%ROOT%\ide\build\hivemind-ide.exe"
)
if not exist "%EXE%" (
  set "EXE=%ROOT%\build\hivemind-ide.exe"
)
if not exist "%EXE%" (
  echo Erro: executavel da HiveMind IDE nao encontrado em "%ROOT%\ide\build\" nem em "%ROOT%\build\".
  echo Rode "cd ide && wails build" primeiro.
  exit /b 1
)
start "" "%EXE%" "%CD%"
