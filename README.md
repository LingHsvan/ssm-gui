<p align="center">
  <strong>SSM GUI — 个人修改版</strong><br>
  基于 Web 的音游自动打歌与谱面解析控制台(BanG Dream! / Project Sekai)
</p>

# 关于本仓库

这是 [hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui) v3.6.1 的**个人修改版 fork**(上游则源自 [kvarenzn/ssm](https://github.com/kvarenzn/ssm)),遵循同一许可 **GPL-3.0-or-later**。

本仓库只做加法和修复,不改变上游的使用方式;所有新增功能围绕一件事:**让"识别当前歌曲 → 载入 → 开打"这条链路更省事**。

## 相对上游的改动

### 🎯 识别歌曲按钮
- 一键截取当前游戏画面,OCR 识别选歌/编队界面上的歌曲标题,自动匹配曲库并**回填歌曲 ID**(歌名条与可用难度直接出现,无需手动输入);
- 引擎为 go-ocr + PP-OCR(mobile)模型,模型文件放在可执行文件旁的 `paddle_weights/`(det.onnx / rec.onnx / keys.txt / onnxruntime.dll);
- 无 scrcpy 会话时自动走 `adb screencap` 兜底(HID 后端也能用);adb 冷启动自动轮询等待,不再误报"无设备";
- 多人房场景友好:已载入待开始状态下点识别,会自动释放旧加载(日志有提示),识别后重新「载入并准备」即可换歌。

### 🛠 识别调试面板
- 拖动 X/Y/W/H 滑条,**红框实时跟随**;松手后自动刷新该区域的裁剪图、OCR 原文与匹配结果;
- 「保存 ROI」一键写入配置(按游戏模式 bang/pjsk 分别记忆);
- 裁剪区域接近纯色时会提示检查设备是否亮屏解锁、是否停留在游戏界面。

### 🔴 Kill ADB 按钮
- 接通了上游预留但未接线的 `killAdbServer`:使用 HID 前一键关闭 adb server,避免 USB 接口占用导致 "HID device not found"。

### 其他
- 曲库列表本地缓存优先、后台静默刷新,打开即秒出(断网不影响);
- 修复并发识别互相杀死 adb server、adb 冷启动设备枚举竞态等问题;
- 界面文案:简中 / 繁中 / 日 / 英。

## 构建(Windows)

```bat
:: 1. 前端(Vite + Tailwind,需 Node 18+)
cd guirontend
npm install
npm run build

:: 2. 后端(需 MSYS2 mingw64:ffmpeg、libusb、pkgconf、gcc;详见 .github/workflows/release.yml)
cd ../..
set CGO_ENABLED=1
set PKG_CONFIG_PATH=C:\msys64\mingw64\lib\pkgconfig
set PATH=C:\msys64\mingw64in;%PATH%
go build -ldflags "-X main.SSM_VERSION=fork" -o ssm-gui.exe
```

生成的 `ssm-gui.exe` 需要 `gui/frontend/dist` 已构建(go:embed),以及运行目录旁的 FFmpeg / libusb 运行库 DLL。

## 使用概要

1. 平板/手机开启 USB 调试并连接(或使用支持 AOA 的 HID 模式);
2. Settings 添加设备序列号与分辨率(短边为宽);
3. 首次使用先在歌曲识别调试面板中,把红框校准到编队界面的标题位置并保存;
4. 之后流程:编队界面 → 🎯 识别歌曲 → 选难度 → ▶ 载入并准备 → 演出开始时点 START。

详细的游戏素材解包、配置说明请参考[上游仓库文档](https://github.com/hj6hki123/ssm-gui)。

## 已知限制

- 识别使用 adb,因此 HID 会话激活期间需要先释放(流程已自动化);多分辨率下 ROI 需按设备分别校准;
- PJSK 曲库数据来源为 Sekai-World 的 GitHub 在线文件,离线时如无本地缓存不可用;
- 项目用途为学习研究,使用可能违反游戏服务条款,风险自负。

## 致谢与许可

- 上游核心与架构:[kvarenzn/ssm](https://github.com/kvarenzn/ssm)、[hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui)
- 视觉方案参考:[juluobaka/ssm_GUI_plus](https://github.com/juluobaka/ssm_GUI_plus)、[hj6hki123/MaestroMiner](https://github.com/hj6hki123/MaestroMiner)
- OCR:[PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR) 模型 + [getcharzp/go-ocr](https://github.com/getcharzp/go-ocr) + [onnxruntime](https://github.com/microsoft/onnxruntime)
- 本仓库与上游一样以 **GPL-3.0-or-later** 提供,修改版同样开源。
