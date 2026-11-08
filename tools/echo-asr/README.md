# Japanese speech recognition on Echo Show 5

In **Setup → Voice assistant**, set **Answered by** to **Muse** and **Language** to `ja`. Set **Speech to text (host:port)** to choose the recognizer, then save.

| Recognizer | Speech to text |
| --- | --- |
| Moonshine on this Echo | `127.0.0.1:50024` |
| Whisper on your PC | The PC's LAN address and Wyoming port, e.g. `192.0.2.10:50024` |
| Muse voice recognition | Leave empty |

Hey Muse displays the transcript and sends it to Muse as text. If the configured recognition service fails, Hey Muse normally sends the captured audio to Muse as a voice note. With the local home dashboard configured (`idle-home.json`), it asks the user to repeat instead, so uncertain appliance commands do not bypass the local matcher. An empty transcript ends the turn. The wake word is configured separately.

## Build the Echo services

The Echo Show 5 (checkers) runs 32-bit userland on a 64-bit kernel. The bundle contains an AArch64 CPU worker using sherpa-onnx 2.0.1, a loopback Wyoming server, a Silero VAD service and a separate AArch64 dynamic loader. Silero ends capture after 0.8 seconds of non-speech. The existing energy detector takes over if the socket is unavailable.

Use Ubuntu 24.04 (WSL works), Go compatible with `echod/go.mod`, curl, bzip2, and the Ubuntu cross toolchain (`gcc-aarch64-linux-gnu`, `libc6-dev-arm64-cross`, `libstdc++6-arm64-cross`). From the repository root:

```sh
tools/echo-asr/build-bundle.sh echo-asr.tar.gz
ECHO_ASR=1 tools/linux/deploy-rootfs.sh --out rootfs.tar.gz
```

The second command requires the usual project rootfs inputs and helpers. It adds the two BusyBox init services to the image. The first command downloads the pinned runtime, Silero and both Japanese Moonshine archives, checks SHA-256, and packages the models with their licenses. The Japanese tiny and base weights carry the Moonshine Community License for non-commercial use. [Model downloads](https://k2-fsa.github.io/sherpa/onnx/moonshine/models-v2.html) · [Model license](https://github.com/moonshine-ai/moonshine/blob/main/LICENSE)

## Install and switch models

Copy both archives to the device, for example into `/data/local/tmp`. For a first install, run in the Echo's root shell:

```sh
tar -xzf /data/local/tmp/echo-asr.tar.gz -C /data
slotctl install /data/local/tmp/rootfs.tar.gz
slotctl status
```

Boot the newly installed slot using the project's normal update procedure. The models persist under `/data/echo-asr/models`. First startup selects **tiny**. After boot, check the service and switch between the installed models:

```sh
cd /data/echo-asr
./echo-asr-server -check
./select-model base
# Once init has restarted the server, the info response shows models/base.
./echo-asr-server -check
./select-model tiny
```

Changing the model restarts the recognizer, so switch between conversations. The selection survives reboot. Existing installations with only `/data/echo-asr/model` can keep using that directory until the new model bundle is installed. To transcribe a test file on the Echo, use `./echo-asr-server -check -wav sample.wav` with a 16 kHz, 16-bit mono PCM WAV of up to 30 seconds.

## Run Whisper on a PC

In a Python environment on the PC, install the runtime. For an NVIDIA GPU also install the CUDA 12 libraries:

```sh
python -m pip install faster-whisper==1.2.1 nvidia-cublas-cu12 nvidia-cudnn-cu12
export LD_LIBRARY_PATH=$(python -c 'import nvidia.cublas.lib, nvidia.cudnn.lib; print(nvidia.cublas.lib.__path__[0]+":"+nvidia.cudnn.lib.__path__[0])')
python tools/echo-asr/pc/recognizer.py --bind PC_LAN_IP --allow ECHO_LAN_IP --device cuda
```

The defaults are `--model large-v3-turbo`, `--device auto` (CUDA when `ctranslate2` sees a GPU, otherwise CPU) and `--compute-type` `float16` on CUDA or `int8` on CPU. The model downloads on first start (about 1.6 GB). To run offline or on a CPU-only PC, download a model once and pass its directory:

```sh
hf download Systran/faster-whisper-small --revision 536b0662742c02347bc0e980a01041f333bce120 --local-dir whisper-small
python tools/echo-asr/pc/recognizer.py --model-dir whisper-small --device cpu --bind PC_LAN_IP --allow ECHO_LAN_IP
```

Replace the two IP placeholders with the addresses on your LAN. The server defaults to port 50024. Allow the Echo to reach that port and select the PC endpoint in Setup. To return to on-device recognition, set the endpoint back to `127.0.0.1:50024`.
