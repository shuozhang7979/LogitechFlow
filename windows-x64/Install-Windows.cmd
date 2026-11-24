@echo off
pushd "%~dp0"
for %%f in (Oink-*.msi) do (
  echo Installing %%f ...
  msiexec.exe /i "%%f"
  goto :done
)
:done
popd
