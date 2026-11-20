; HiveMind Windows Installer (Inno Setup 6)
; Compiles HiveMind IDE (hivemind-ide.exe) and CLI (hivemind.exe) into a single Windows installer.

#ifndef AppVersion
  #define AppVersion "0.1.0"
#endif
#ifndef RepoRoot
  #define RepoRoot "..\.."
#endif
#ifndef IdeExePath
  #define IdeExePath RepoRoot + "\ide\build\bin\hivemind-ide.exe"
#endif
#ifndef CliExePath
  #define CliExePath RepoRoot + "\dist\hivemind.exe"
#endif
#ifndef IconPath
  #define IconPath RepoRoot + "\assets\brand\hivemind.ico"
#endif
#ifndef OutputDir
  #define OutputDir RepoRoot + "\dist"
#endif

[Setup]
AppId={{8F6E2A14-4C9B-4B9E-9A51-7F2D9E4A10B2}
AppName=HiveMind
AppVersion={#AppVersion}
AppVerName=HiveMind {#AppVersion}
AppPublisher=Gilvan Borges
AppPublisherURL=https://github.com/gilvan-Borges/HiveMind
AppSupportURL=https://github.com/gilvan-Borges/HiveMind/issues
AppUpdatesURL=https://github.com/gilvan-Borges/HiveMind/releases
DefaultDirName={autopf}\HiveMind
DefaultGroupName=HiveMind
AllowNoIcons=yes
LicenseFile={#RepoRoot}\LICENSE
OutputDir={#OutputDir}
OutputBaseFilename=HiveMindSetup-x64
SetupIconFile={#IconPath}
UninstallDisplayIcon={app}\hivemind-ide.exe
UninstallDisplayName=HiveMind {#AppVersion}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ChangesEnvironment=yes
CloseApplications=yes
RestartApplications=no
DisableProgramGroupPage=yes
VersionInfoVersion={#AppVersion}.0
VersionInfoProductName=HiveMind
VersionInfoDescription=HiveMind IDE & CLI Installer

[Languages]
Name: "brazilianportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"
Name: "addtopath"; Description: "Adicionar HiveMind (hivemind e hivemind-ide) ao PATH"; GroupDescription: "Integração com o sistema:"
Name: "contextmenu"; Description: "Adicionar ""Abrir com HiveMind IDE"" ao menu de contexto de pastas"; GroupDescription: "Integração com o sistema:"

[Files]
Source: "{#IdeExePath}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#CliExePath}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#RepoRoot}\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#RepoRoot}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\HiveMind IDE"; Filename: "{app}\hivemind-ide.exe"; IconFilename: "{app}\hivemind-ide.exe"
Name: "{group}\Desinstalar HiveMind"; Filename: "{uninstallexe}"
Name: "{autodesktop}\HiveMind IDE"; Filename: "{app}\hivemind-ide.exe"; IconFilename: "{app}\hivemind-ide.exe"; Tasks: desktopicon

[Registry]
; Explorer context menu on folders
Root: HKA; Subkey: "Software\Classes\Directory\shell\HiveMindIDE"; ValueType: string; ValueName: ""; ValueData: "Abrir com HiveMind IDE"; Flags: uninsdeletekey; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Directory\shell\HiveMindIDE"; ValueType: string; ValueName: "Icon"; ValueData: """{app}\hivemind-ide.exe"",0"; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Directory\shell\HiveMindIDE\command"; ValueType: string; ValueName: ""; ValueData: """{app}\hivemind-ide.exe"" ""%1"""; Tasks: contextmenu

; Explorer context menu on folder background
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\HiveMindIDE"; ValueType: string; ValueName: ""; ValueData: "Abrir com HiveMind IDE"; Flags: uninsdeletekey; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\HiveMindIDE"; ValueType: string; ValueName: "Icon"; ValueData: """{app}\hivemind-ide.exe"",0"; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\HiveMindIDE\command"; ValueType: string; ValueName: ""; ValueData: """{app}\hivemind-ide.exe"" ""%V"""; Tasks: contextmenu

[Run]
Filename: "{app}\hivemind-ide.exe"; Description: "{cm:LaunchProgram,HiveMind IDE}"; Flags: nowait postinstall skipifsilent

[Code]
procedure SHChangeNotify(wEventId: Longint; uFlags: Cardinal; dwItem1, dwItem2: Longint);
  external 'SHChangeNotify@shell32.dll stdcall';

function EnvRegRoot: Integer;
begin
  if IsAdminInstallMode then
    Result := HKEY_LOCAL_MACHINE
  else
    Result := HKEY_CURRENT_USER;
end;

function EnvRegSubKey: string;
begin
  if IsAdminInstallMode then
    Result := 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment'
  else
    Result := 'Environment';
end;

function NormalizePathEntry(const S: string): string;
begin
  Result := Uppercase(RemoveBackslashUnlessRoot(Trim(S)));
end;

procedure AddDirToPath(const Dir: string);
var
  OldPath, NewPath, NormDir, Remaining, Entry: string;
  P: Integer;
  Found: Boolean;
begin
  OldPath := '';
  RegQueryStringValue(EnvRegRoot, EnvRegSubKey, 'Path', OldPath);
  NormDir := NormalizePathEntry(Dir);
  Remaining := OldPath;
  Found := False;

  while Remaining <> '' do
  begin
    P := Pos(';', Remaining);
    if P > 0 then
    begin
      Entry := Copy(Remaining, 1, P - 1);
      Delete(Remaining, 1, P);
    end
    else
    begin
      Entry := Remaining;
      Remaining := '';
    end;
    if NormalizePathEntry(Entry) = NormDir then
    begin
      Found := True;
      Break;
    end;
  end;

  if not Found then
  begin
    if (OldPath <> '') and (OldPath[Length(OldPath)] <> ';') then
      NewPath := OldPath + ';' + Dir
    else
      NewPath := OldPath + Dir;
    RegWriteExpandStringValue(EnvRegRoot, EnvRegSubKey, 'Path', NewPath);
  end;
end;

procedure RemoveDirFromPath(const Dir: string);
var
  OldPath, NewPath, NormDir, Remaining, Entry: string;
  P: Integer;
begin
  if not RegQueryStringValue(EnvRegRoot, EnvRegSubKey, 'Path', OldPath) then
    Exit;

  NormDir := NormalizePathEntry(Dir);
  Remaining := OldPath;
  NewPath := '';

  while Remaining <> '' do
  begin
    P := Pos(';', Remaining);
    if P > 0 then
    begin
      Entry := Copy(Remaining, 1, P - 1);
      Delete(Remaining, 1, P);
    end
    else
    begin
      Entry := Remaining;
      Remaining := '';
    end;

    if (Trim(Entry) <> '') and (NormalizePathEntry(Entry) <> NormDir) then
    begin
      if NewPath <> '' then
        NewPath := NewPath + ';';
      NewPath := NewPath + Entry;
    end;
  end;

  RegWriteExpandStringValue(EnvRegRoot, EnvRegSubKey, 'Path', NewPath);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
begin
  if CurStep = ssPostInstall then
  begin
    if WizardIsTaskSelected('addtopath') then
      AddDirToPath(ExpandConstant('{app}'));
    SHChangeNotify($08000000, $0000, 0, 0);
    Exec('ie4uinit.exe', '-show', '', SW_HIDE, ewNoWait, ResultCode);
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveDirFromPath(ExpandConstant('{app}'));
end;
