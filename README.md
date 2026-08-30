# ssm-gui(个人修改版)

基于 [hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui)(上游 [kvarenzn/ssm](https://github.com/kvarenzn/ssm))的个人 fork,GPL-3.0。

## 改动

- 🎯 **识别歌曲**:一键截图 OCR 识别当前歌曲,自动填 ID(PP-OCR 模型,放 `paddle_weights/`);HID 模式自动让路,adb 冷启动自动等待
- 🛠 **识别调试**:滑条拖动红框实时跟随,松手自动识别,ROI 按游戏模式保存
- 🔴 **Kill ADB 按钮**:接通上游预留接口,解决 HID 被占用问题
- 曲库列表本地优先秒开;修复并发识别、adb 竞态等稳定性问题

## 构建

前端 `gui/frontend`:`npm install && npm run build`;后端根目录:CGO + MSYS2 mingw64(ffmpeg/libusb),详见 `build.bat` 与 `.github/workflows/release.yml`。

使用与素材解包说明见[上游文档](https://github.com/hj6hki123/ssm-gui)。

## 许可

GPL-3.0-or-later,与上游一致。学习研究用途,风险自负。
