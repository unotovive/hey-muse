# Hey Muse（日本語）

Amazon Echo Show 5 を、Meta の AI エージェント [Muse](https://gadgets.muse.ai) の声の窓口にする個人用プロジェクトです。この派生は、日本の家庭向けに次を加えています。

- **日本語の音声認識**: 母艦（WSL の Whisper）で聞き取り、母艦が止まっていれば録音を Muse に送って Muse に認識させます。
- **待機画面**: Google カレンダーの予定、Nature Remo の温度・湿度と家電操作（SwitchBot も従来どおり使えます）。
- **地震情報の割り込み表示**: P2P 地震情報を端末が直接受信し、全画面で表示します。
- **在室検知と「手で覆って停止」**: 無人で消灯し、手をかざしてアラームや Muse の読み上げを止めます。

> Meta や Amazon の製品ではなく、どちらも関与していません。Muse の利用は Meta の [Gadget SDK Terms](https://gadgets.muse.ai/sdk-terms) に従います。ベースは HuskerMinion の [TECHO5](https://github.com/HuskerMinion/techo5)（MIT）で、元の README は [README.upstream.md](README.upstream.md) にあります。

## 日本語の手順書

- [セットアップ](docs/ja/setup.md): 母艦の常駐（第 3 部）、Google カレンダー（第 4 部）、在室検知（第 5 部）、地震情報（第 6 部）、端末設定の配置（第 7 部）、Muse の契約とペアリング（第 8 部）。
- [リリース手順](docs/ja/release.md)
- [受け入れ記録](docs/ja/acceptance.md): 実施した確認と、実機待ちの確認。
- 本体連携の詳細: [tools/standalone-home/README.md](tools/standalone-home/README.md)

現在のリリースは `v1.6.30-ja.2` です。以下は上流の流れを引き継いだ英語の説明です。

---

# Hey Muse

An Amazon Echo Show 5 that answers as [Muse](https://gadgets.muse.ai), Meta's AI agent. Say "Hey
Muse", ask something, and your own Muse answers out loud, with its character on the screen. No
Alexa, no Amazon cloud, and no Home Assistant needed.

This is a fork of [TECHO5](https://github.com/HuskerMinion/techo5) by HuskerMinion, which replaces
the Echo's Android with a small Linux and one Go daemon. TECHO5 does the hard part: the kernel, the
microphones, echo cancellation, the wake word, the screen. This fork adds Muse as one more choice for
who answers. Everything else about the device is TECHO5's, and its own README is kept here as
[README.upstream.md](README.upstream.md).

> **Not made by Meta or Amazon.** This is a hobby project. Neither company made it or endorses it.
> Using Muse this way is covered by Meta's [Gadget SDK Terms](https://gadgets.muse.ai/sdk-terms).

## What this fork adds

- **Muse as the voice assistant.** A third choice beside Home Assistant and TECHO5's direct
  pipeline, picked on the setup page. Home Assistant keeps working for anyone who uses it.
- **Pairing with the Muse app over the Echo's own Bluetooth.** No other device is involved. The Echo
  then shows up in the Muse app's device list like any Muse gadget.
- **A voice with no server at home.** Answers are spoken through an OpenAI-style speech endpoint, or
  by a built-in voice (SVOX Pico) that needs no key and no internet.
- **Japanese speech recognition.** Use Moonshine tiny or base on Echo Show 5, or Whisper on a PC.
  [Set up recognition and switch models](tools/echo-asr/README.md).
- **A "Hey Muse" wake word,** beside TECHO5's own, chosen on the device.
- **Things Muse can do on the device.** Set timers and alarms, change the volume, play the radio
  and show a few screen pages, asked aloud or from the Muse app.
- **The Muse character on screen** during a turn, if you generate it from your own copy of Meta's
  SDK. The character is Meta's and is not in this repository.
- **A Usage screen.** A clock style that shows meters sent from your computer, made for a Claude
  Code plan's usage. See [docs/usage-screen.md](docs/usage-screen.md).

This branch also includes Japanese speech and display support, per-wake side chats, a calendar
and weather dashboard, and optional local SwitchBot control through a PC bridge.
See [the consolidated changes and setup](docs/muse-local-changes.md). Optional
[Jev intent routing](docs/jev-read-only.md) handles Japanese weather and calendar paraphrases.

## What you need

- An Echo Show 5 that TECHO5 supports, with its bootloader unlocked.
  [TECHO5's getting started guide](docs/getting-started.md) covers which models and how.
- A Muse subscription, the Muse app, and an SDK token of your own from
  [gadgets.muse.ai](https://gadgets.muse.ai/settings/sdk-tokens). The token is personal. Never
  publish it.
- Optional: a key for an OpenAI-style speech endpoint, for a more natural voice.

## Install

On an Echo Show 5 2nd gen with its bootloader unlocked and LineageOS 18.1 on it, which is where
[TECHO5's getting started guide](docs/getting-started.md) leaves you:

```sh
git clone https://github.com/unotovive/hey-muse
cd hey-muse
python3 tools/install-show.py
```

It needs Python 3, `adb` and `fastboot`, on Windows, Linux or macOS. It downloads the latest release
of this fork, checks it against the fork's signing key, and asks once before it erases LineageOS.
[docs/install.md](docs/install.md) is TECHO5's guide to the same installer.

A device already running upstream TECHO5 does not move to this fork by itself, since it trusts
upstream's key. [docs/releasing-the-fork.md](docs/releasing-the-fork.md) says how to move one over.

Then follow [docs/muse.md](docs/muse.md): choose Muse on the setup page, paste your token, pair in
the Muse app, and pick **Hey Muse** as the wake word on the device (Settings, Sound, Wake word). A
new device listens for "Alexa" until you do. A device on this fork finds later releases of it by itself.

The release carries a boot image for the Show 5 2nd gen only, the one board this was tried on.

## What your voice does

- The wake word is detected on the device. Nothing is sent while it waits.
- After the wake word, the configured local recognizer can transcribe your speech. Local weather
  and configured appliance commands are handled before Muse. Other transcripts go to Muse as text.
  Without local recognition, audio goes to Muse as a voice note and is stored in your account.
  See [speech recognition and fallback behavior](tools/echo-asr/README.md).
- With a cloud voice, the text of each answer goes to the speech provider. With the built-in voice,
  nothing else leaves the device.

In the state file, `brain.muse.chat` chooses where turns go: empty or `"main"` uses your main
chat (the default), `"new"` creates a fresh side chat for each wake and keeps follow-ups in that chat, and any other value reuses that
session id. Set the follow-up time on the active wake word to 8 seconds and the follow-up count to 0 to keep listening after each reply until nobody answers. Fixed ids must contain 1–64 letters, digits or dashes; invalid values fall back to main.

[docs/muse.md](docs/muse.md) has the details.

## What was tested

On one Echo Show 5 2nd gen (cronos), in October 2026:

- Installing the image and booting from it, and reconnecting to Muse after a reboot.
- Pairing from the Muse app on an iPhone.
- Wake word, question, spoken answer, with the cloud voice and with the built-in voice.
- The character on screen.
- The "Hey Muse" wake word, with one voice.
- Muse changing the volume and setting an alarm, asked aloud and from the Muse app.

Not tested: the timer, radio and screen commands (only the volume and an alarm were tried), pairing
from Android, the Show 5 1st gen, the Show 8, the Spot and the Dot, pairing while
Bluetooth earbuds are connected, the setup page's pairing button (pairing was done from the command
line), and this fork with a Home Assistant server attached.

An answer took 7 to 11 seconds from the end of the question, most of it Muse thinking.

## Credits

- [TECHO5](https://github.com/HuskerMinion/techo5) by HuskerMinion, which this is a fork of, and
  [EchoLocal](echod/README.upstream.md) by Yuri Gelfand, which TECHO5 is built on. Both MIT.
- Meta's [Muse Gadget SDK](https://github.com/facebookincubator/muse-gadget-sdk), Apache 2.0. The
  Muse client and the pairing code here are ports of it.
- SVOX Pico, Apache 2.0, for the built-in voice.

[NOTICE](NOTICE) lists everything and its license.

## License

MIT, as TECHO5 is. See [LICENSE](LICENSE), [echod/LICENSE](echod/LICENSE) and [NOTICE](NOTICE).
