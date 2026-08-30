> [!IMPORTANT]
> **As the core features are now complete, the main branch of this project will only receive minor bug fixes and no major functional changes.**
> 
> **For new feature development, please head over to the [test/automation](https://github.com/hj6hki123/ssm-gui/tree/test/automation) branch.**
> 
> **This branch is under active development, focusing on a fully autonomous 'unattended mode' to achieve a completely hands-off user experience.**
<p align="center">
    <a href="https://github.com/hj6hki123/ssm-gui">
        <img src="imgs/page.png" alt="ssm-gui-banner"/>
    </a>
    <br>
    <strong>A Web-based GUI for automated mobile rhythm game playback and chart parsing.</strong>
</p>


# SSM Web GUI (Star Stone Miner Web GUI Version)

This project is an extended branch based on the core architecture of [kvarenzn/ssm](https://github.com/kvarenzn/ssm).
~~Given that the original author has stopped development (I think?)~~ , once again express my gratitude to kvarenzn for the excellent work. I’ve basically just wrapped a simple GUI shell  around the original core architecture to make the gameplay experience even more convenient and user-friendly.

**If you're looking for an easier way to play, give this version a try!**

**Support for BanG Dream and Project Sekai: Colorful Stage**



##  What's new

### 🎵 Smart Song Search
![Keyword Search](/imgs/retrival.png "retrival")
- Real-time search across the full Bestdori library
- One-click difficulty selection (EASY → SPECIAL)
- Still supports manual Song ID and custom `.txt` chart paths for the power users

### ▶️ Playback Control Panel
![Control Panel](/imgs/paly_page.png "Control Panel")
- **Now Playing card** — jacket art, song title, band, difficulty, all in one glance
- **Restart instantly** — hit Restart to re-arm the current song; press Start again without re-loading anything
- **Offset adjustment** — fine-tune timing on the fly with keyboard shortcuts





## Quick Start

### English

1. **Download**
    - Get the latest package from [Releases](https://github.com/hj6hki123/ssm-gui/releases) and extract it.
    - If you already have the original `ssm` project, you can place `ssm-gui.exe` in the same folder.

2. **Start the program**
    - Double-click `ssm-gui.exe`, or run:
      ```bash
      ./ssm-gui.exe
      ```
    - The UI should open automatically at `http://127.0.0.1:8765`.
    - If it does not open, visit the address manually in your browser.

3. **Prepare your phone**
    - Connect your phone to your PC with a USB cable.
    - Enable **USB debugging / ADB debugging** on the phone.

4. **Copy game resources to PC**
    - Copy and extract the game resource pack to your computer.
    - Also copy the device data directory:
      - BanG Dream example:
         ```bash
         adb pull /sdcard/Android/data/jp.co.craftegg.band/files/data/
         ```
      - Path format:
         `/sdcard/Android/data/{game_package_name}/files/data/`

5. **Extract assets**
    - Open the **Extract Assets** page in the sidebar.
    - Enter the full path to the data folder you just pulled, then click **Extract**.
    - This unpacks the charts into an `assets/` folder. Skipping this step causes "Musicscore not found".
    - Redo this whenever you add new songs or difficulties.

6. **Set up device in GUI**
    - Open the **Settings** page.
    - Add your device (serial number can be auto-detected or selected from dropdown).
    - Choose connection type: **HID** or **ADB**.

7. **Load song and start playback**
    - In the main flow, go through: **Song Setup -> Play Control -> Start**.
    - When the first note reaches the judgement line, press **Start** (or keyboard **Enter** / **Space**).
    - If timing is early/late, adjust **Offset/Delay** and retry.

> Legacy command-line usage is still supported. You can append original CLI parameters as before.
> See [kvarenzn's Usage Guide](https://github.com/kvarenzn/ssm/blob/main/docs/USAGE.md).


1. **下载与解压**

   * 从 [Releases](https://github.com/hj6hki123/ssm-gui/releases) 下载最新版本并解压。
   * 如果你已经有原版 `ssm` 项目，直接把 `ssm-gui.exe` 放到同一文件夹即可。

2. **启动程序**

   * 直接双击 `ssm-gui.exe`，或用终端运行：

     ```bash
     ./ssm-gui.exe
     ```
   * 程序会尝试自动打开浏览器到 `http://127.0.0.1:8765`。
   * 如果没有自动打开，请手动输入网址。

3. **连接并准备手机或模拟器**

   * 手机请用 USB 线连接电脑；如果使用模拟器，请先确认 ADB 已启用。
   * 在手机上开启 **USB 调试 / ADB 调试**。
   * 可使用以下命令确认设备是否已连接：

     ```bash
     adb devices
     ```

4. **准备游戏资源**

   * 将游戏资源包复制到电脑并解压。
   * 同时把手机中的数据目录复制到电脑：

     * BanG Dream 示例：

       ```bash
       adb pull /sdcard/Android/data/jp.co.craftegg.band/files/data/
       ```
     * 通用路径：
       `/sdcard/Android/data/{游戏包名}/files/data/`

5. **解包资源**

   * 打开侧边栏的 **Extract Assets** 页面。
   * 填入刚刚 pull 下来的数据目录的完整路径，点击 **Extract**。
   * 这会把谱面解包到 `assets/` 文件夹。跳过这步会出现「未找到谱面 / Musicscore not found」。
   * 每次新增歌曲或难度后都要重新解包。

6. **在 GUI 中设置设备**

   * 进入 **Settings** 页面添加设备。
   * 序列号可以自动检测，或从下拉菜单选择。
   * 连接方式选择 **HID** 或 **ADB**。

7. **选歌并开始**

   * 按流程操作：**Song Setup -> Play Control -> Start**。
   * 当第一个音符接近判定线时，按 **Start**（或键盘 **Enter** / **Space**）。
   * 如果时机偏早或偏晚，可以调整 **Offset/Delay** 后重试。

> 仍可使用传统命令行参数方式启动。
> 详细参数请参考 [kvarenzn 的使用指南](https://github.com/kvarenzn/ssm/blob/main/docs/USAGE.md)。

## Alternative Solutions

If you are currently looking for a reliable auto-play/auto-click solution, I highly recommend checking out [juluobaka's ssm_GUI_plus](https://github.com/juluobaka/ssm_GUI_plus). Their project offers a much more stable screen mirroring implementation along with a visually calibrated auto-hit feature. 

I will also be integrating their core methods into the fully automated workflow of the MAA branch in the near future to further enhance overall stability.

## Disclaimer
This program was heavily developed with the assistance of AI. Please use it at your own discretion and feel free to report any unexpected bugs or issues.

> [!IMPORTANT]
> **This project is developed for personal learning and research purposes only. The stability and applicability of its functions are not guaranteed.**
>
> * **Non-Affiliation**: This project is an independent third-party tool and is **not** affiliated with, authorized by, or associated with any game developers, publishers, or related organizations.
> * **Risk of Use**: Use of this project may violate the service terms of the games or platforms involved, potentially leading to account suspension, bans, or data corruption.
> * **Limitation of Liability**: The author assumes no responsibility for any consequences resulting from the use of this project. Users are advised to evaluate the risks and use the software with caution.

## Future Projects
1. Mobile Porting & Deployment: Porting the application to mobile devices for use on non-rooted hardware (leveraging ADB tools such as Shizuku).

2. Automated Rhythm Game Playback: Implementation of image recognition for automated gameplay in rhythm games.

---

## 📜 License & Credits

* **Core Play Logic & Chart Parsing**: Credited to the original author [kvarenzn](https://github.com/kvarenzn/ssm).
* **Web GUI Implementation**: Custom integrated control panel developed specifically for this branch.
* This project is licensed under the **GPL-3.0-or-later** license.
