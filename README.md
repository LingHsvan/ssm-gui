# ssm-gui(个人修改版)

基于 [hj2693505022/ssm-gui](https://github.com/hj2693505022/ssm-gui)(本项目在其Fork上继续发展)，同样是[hj6hki123/ssm-gui](https://github.com/hj6hki123/ssm-gui)(上游 [kvarenzn/ssm](https://github.com/kvarenzn/ssm))的个人 fork,GPL-3.0。

## 改动

- 🎯 **识别歌曲**:一键截图 OCR 识别当前歌曲,自动填 ID(PP-OCR 模型,放 `paddle_weights/`);HID 模式自动让路,adb 冷启动自动等待
- 🛠 **识别调试**:滑条拖动红框实时跟随,松手自动识别,ROI 按游戏模式保存
- 🔴 **Kill ADB 按钮**:接通上游预留接口,解决 HID 被占用问题
- 🔌 **设备连接增强**:Auto Detect 自动拉起 adb server(无需事先跑过 `adb devices`);序列号只作优先级:留空自动选用已配置且在线的设备(多台时按序列号确定性选择),已选设备离线时自动回退到其他已配置设备,换设备不用改设置;检测到的设备自动登记进「设备管理」(含 `wm size` 分辨率)
- 🔍 **OCR 模糊匹配**:支持部分识别(メオ→ロメオ)与片假名、形近汉字混淆(上海ハニー↔上海八二一),容忍零宽字符、多余空格等 OCR 噪声;唯一候选自动填入,多个候选时在「识别歌曲」下方列出,点击即可选择
- 🔎 **搜索字形折叠**:搜索自动折叠 简/繁/日新字体 及形近片假名,输入「极乐」「口三才」也能搜到「極楽」「ロミオ」,无需日文输入法
- 曲库列表本地优先秒开;修复并发识别、adb 竞态等稳定性问题

## 构建

前端 `gui/frontend`:`npm install && npm run build`;后端根目录:CGO + MSYS2 mingw64(ffmpeg/libusb),详见 `build.bat` 与 `.github/workflows/release.yml`。

使用与素材解包说明见[上游文档](https://github.com/hj6hki123/ssm-gui)。

## 许可

GPL-3.0-or-later,与上游一致。学习研究用途,风险自负。
