[Setup]
AppName=TNT
AppVersion=1.1.0
DefaultDirName={autopf}\TNT
DefaultGroupName=TNT
OutputDir=output
OutputBaseFilename=TNT
UninstallDisplayName=TNT
UninstallDisplayIcon={app}\tnt.exe
Compression=lzma2
SolidCompression=no

[Files]
Source: "tnt.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\TNT"; Filename: "{app}\tnt.exe"
Name: "{group}\Uninstall TNT"; Filename: "{uninstallexe}"

[Run]
Filename: "{app}\tnt.exe"; Description: "Launch TNT"; Flags: nowait postinstall skipifsilent
