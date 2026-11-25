# DeskSpan

一套键鼠控制同一局域网里的多台 Windows 和 Mac，每台电脑可以有多块显示器。鼠标越过桌面边缘时键盘跟随切换。可以把文件从资源管理器直接拖到访达中的文件夹，也可以在一端复制文本或图片，到另一端粘贴。

本仓库的定制 Oink 运行时基于上游 Oink 0.3.6。定制版本从 1.0.0 起算，当前版本为 1.0.1。

DeskSpan 0.1.0 是独立的开源下游项目。键鼠、剪贴板、原生文件拖放和日常图形界面由 Oink 提供。DeskSpan 新增中文多屏排列向导、组内设备状态查询和批量布局命令，通过已安装的本机代理保存布局。[上游功能与兼容性](https://github.com/oinkvm/oink/blob/v0.3.6/README.zh-CN.md)

## 在每台电脑上安装

1. 选择本机的 DeskSpan ZIP 并解压。Windows 选 `windows-x64`。Mac 的“关于本机”若显示 Apple 芯片，选 `macos-arm64`，若显示 Intel 处理器，选 `macos-x64`。
2. 双击“安装运行时”。它从上游正式发布页获取对应的 MSI 或 DMG，校验 SHA-256 后打开安装程序。Mac 上按磁盘映像中的提示安装到“应用程序”。已有 Oink 0.3.6 的电脑可直接打开 Oink。离线安装时，将对应的原版安装包放在“安装运行时”旁边再双击即可。[正式版与校验值](https://github.com/oinkvm/oink/releases/expanded_assets/v0.3.6)
3. 将电脑接入可互访的同一局域网，分别启动 Oink。在设备列表添加对方，按提示在一端输入另一端显示的六位配对码。其余电脑逐台加入。自动发现不到对方时，在添加设备的界面输入对方的局域网 IP。
4. Mac 首次运行时，按 Oink 的引导开启“辅助功能”和所需的“输入监控”。要向 Mac 拖入文件，按引导启用“文件提供服务”，在弹出的访达窗口点击“启用”。[权限引导](https://github.com/oinkvm/oink/blob/v0.3.6/apps/oink/src/onboarding/OnboardingPermissions.tsx)

Windows 运行时支持 x64 的 Windows 10 1809 及以上、Windows 11。Mac 需要 macOS 14.2 及以上。上游注明 Intel Mac 构建和 Windows 10 22H2 之前的版本未经实机验证。Windows 10 LTSC 还需安装 WebView2 Runtime。[系统要求](https://github.com/oinkvm/oink/blob/v0.3.6/README.md)

## 摆好显示器

先在各电脑的系统显示设置里排好自己的屏幕，包括竖屏和高低差。多台电脑配对后，只需在其中一台打开解压目录的“配置多屏”，选择横排或纵排，按实际桌面顺序输入电脑编号。向导会列出每台电脑的屏幕数及预览位置。按 Y 保存到整个设备组。

例如，左侧 Windows 有两屏，右侧 Mac 有内屏和外屏。在向导中选择“横排”，先输入 Windows 的编号，再输入 Mac 的编号。Windows 右侧屏幕的外边缘会与 Mac 左侧屏幕的外边缘相接。第一台电脑的位置固定，各台电脑的屏间偏移和缩放比例一并保留。上下左右混排的桌面可直接在 Oink 的“排列”界面移动各台电脑的屏幕组。

## 使用和验收

1. 将指针依次移过本机的两块屏幕，再越过与另一台电脑相接的边缘。在对端文本编辑器输入几个字，然后移回原电脑，确认键盘焦点也随指针返回。
2. 在 Windows 资源管理器选一个小文件，按住鼠标拖过边缘，在 Mac 访达中打开的目标文件夹松开。打开副本确认内容，检查源文件仍在。换一个文件反向测试。大文件等传输结束后再打开。
3. 在 Windows 用 Ctrl+C 复制一段中文，在 Mac 的文本编辑器用 Command+V 粘贴。反向用 Command+C 和 Ctrl+V。再将一张截图粘贴到支持图片的应用。

若键鼠可用但文件拖入 Mac 失败，先检查文件提供服务。若剪贴板单向可用，检查源电脑的“发送”和目标电脑的“接收”开关。终端中的 `deskspan status` 可查看本机开关、组内链路与每块屏幕的位置。

## 命令行

在解压目录打开终端。下面是 Windows PowerShell 示例，macOS 将 `.\deskspan.exe` 换成 `./deskspan`。设备名称取自第一条命令的输出。名称重复时可使用完整设备 ID。

```powershell
.\deskspan.exe status
.\deskspan.exe setup
.\deskspan.exe arrange row "Windows 台式机" "MacBook"
.\deskspan.exe arrange --apply row "Windows 台式机" "MacBook"
```

`row` 按从左到右的顺序排列，`column` 按从上到下的顺序排列，也可一次列出三台或更多电脑。默认打印预览，`--apply` 将所有位置作为一次布局编辑提交。安装位置自定义时，可用 `--agent` 指定 `oink-agent` 的完整路径。

## 二次开发和分发

### Apple 芯片 Mac 的自用签名

在 Xcode 的 **Settings → Accounts → Personal Team → Manage Certificates** 中创建免费的 **Apple Development** 证书。签名使用本机钥匙串中的证书及私钥。在仓库根目录运行下列命令读取对应的 Team ID。

```sh
python3 oink-0.3.6/packaging/macos/sign_development.py --print-team-id
```

在 GitHub Actions 中运行 **Build Oink for local Mac signing**，输入该 Team ID。也可以推送 `codex/oink-personal-macos-<TEAMID>` 分支来触发构建。下载运行生成的 `oink-macos-arm64-development-staging` 产物，其中包含暂存 ZIP 和 `SHA256SUMS`。

在仓库根目录运行以下命令。`--sha256` 填入上一步 `SHA256SUMS` 中该 ZIP 的校验值，`--output` 指向新的输出目录。

```sh
python3 oink-0.3.6/packaging/macos/sign_development.py \
  --archive "$HOME/Downloads/Oink-1.0.1-macos-arm64-development-staging.zip" \
  --sha256 '<SHA-256>' --team '<TEAMID>' \
  --output "$HOME/Downloads/Oink-1.0.1-personal"
```

把签名后的 `Oink.app` 安装到「应用程序」，按权限引导启用文件提供服务。`/Applications/Oink.app/Contents/MacOS/oink-agent file-provider status` 可查看扩展状态。与 Windows 重新配对后，用大文件分别验证访达粘贴和拖拽。自用构建通过此流程更新，正式分发的 Mac 安装包使用 Developer ID 签名和 Apple 公证。

### DeskSpan 命令行和安装包

DeskSpan 的源代码在平台 ZIP 所附的 `DeskSpan-0.1.0-source.zip` 中，也可以单独获取同名源码包。在源码根目录使用 Go 1.22 或以上及 Python 3.9 或以上运行以下命令。发行脚本使用 Go 标准库交叉编译三个平台包。

```text
go test ./...
go build ./cmd/deskspan
python tools/release.py build
python tools/release.py verify
```

- 修改 `cmd/deskspan/` 可扩展向导、设备状态、布局算法和本机接口调用。修改 `tools/` 可调整安装与打包流程。
- 发布 `dist/` 中的三个平台 ZIP、一个对应源码 ZIP 和 `SHA256SUMS`。每个平台包内也附有同版本源码。原版运行时由安装入口单独获取。办理离线部署时可先运行 `python tools/release.py fetch`，校验好的安装包保存在 `dist/installers/`。
- 源码包中的 `oink-v0.3.6.zip` 保存了完整上游快照。解压后，键鼠输入在 `crates/oink-input`，跨机路由在 `crates/oink-routing`，剪贴板在 `crates/oink-clipboard`，拖放会话在 `crates/oink-agent/src/dnd`。要发布修改过的原生引擎，应在自己的上游分支构建对应平台安装包，更新安装地址、哈希和对应源码，再打 DeskSpan 包。
- 本项目新增代码采用 GPL-3.0-only。再分发时保留 `LICENSE`、`NOTICE`、上游第 7 条附加条款及第三方声明，并随修改版提供对应源码和修改说明。DeskSpan 使用独立名称，上游归属见 `NOTICE`。[上游附加条款](https://github.com/oinkvm/oink/blob/v0.3.6/LICENSE-ADDITIONAL-TERMS.md)
