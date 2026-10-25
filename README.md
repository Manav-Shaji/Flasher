# Flasher

Flasher is a Windows, Linux, and macOS tool for common Android ADB and Fastboot tasks. It provides a terminal interface for selecting a connected device, checking its status, and choosing an image or ROM package.

## What it can do

- Flash `.img` files to **boot** or **recovery**.
- Use **Wipe Super** with a compatible `super_empty.img`.
- Install `.zip` packages through **ADB sideload** in Recovery.
- Show device information and calculate a file's SHA-256 hash.

Flasher uses the device identity reported by ADB or Fastboot; it does not limit the workflow to a built-in model list. This does not mean every ROM or image works on every device. Choose files specifically made for your exact device and intended partition. An unknown compatibility result requires extra confirmation and is not proof that a file is safe. A mismatch or malformed package is blocked.

## Important warnings

> **DANGER — Flashing can permanently brick your device.** A failed or incompatible operation may leave the device unable to boot and may require specialist recovery or hardware repair. You proceed at your own risk.

### Cautions before flashing

- **Back up important data.** Unlocking the bootloader commonly wipes user data; flashing can also erase data.
- **Verify the exact device and variant.** Similar names or a matching codename alone do not prove compatibility. Check the ROM maintainer's instructions and trusted sources.
- **Verify the image and partition.** Use only a boot image for `boot`, a recovery image for `recovery`, and a `super_empty.img` only when the device-specific instructions explicitly require it.
- **Unknown compatibility is not approval.** Confirming the warning does not make an image safe. A wrong image, firmware, partition, or install method can cause a bootloop, data loss, or a hard brick.
- **Do not interrupt an operation.** Keep the device charged and the USB connection stable. Do not unplug, power off, or close Flasher while a command is running.
- **Understand unlocking consequences.** Bootloader unlocking may erase the device, reduce security, or affect warranty. Check your manufacturer's policy and device-specific guidance.
- **Know your recovery path first.** Before proceeding, ensure you have the correct stock firmware/recovery instructions and understand how to recover your exact device. Do not assume reconnecting or retrying will undo a partial flash.

## Install

Download a release from [GitHub Releases](https://github.com/Manav-Shaji/RomFlasher/releases). Install Android platform-tools (`adb` and `fastboot`) and make them available to Flasher.

## Build from source

Install Go 1.25.6 or later, then run:

```sh
git clone https://github.com/Manav-Shaji/RomFlasher.git
cd RomFlasher
go build -o Flasher.exe ./cmd/flasher
```

## Basic use

1. Connect one device and select it in Flasher.
2. For boot or recovery, choose the matching `.img` action and select an image intended for that partition.
3. For a ROM `.zip`, boot the device into Recovery and choose **ADB sideload**.
4. Review the target and warning, then confirm only if you have verified the file is intended for that exact device.

For the CLI, inspect a package with `flasher inspect <package.zip>`. Preview a boot image operation without flashing it using `flasher flash boot <image> --dry-run`. Actual CLI flashing requires explicit acknowledgement; when compatibility is unknown, add `--allow-unverified-compatibility` as well as `--yes`.

See the [Architecture Guide](docs/architecture.md) for design details and the [Hardware Validation Plan](docs/hardware-validation.md) for read-only checks. Do not use the hardware validation procedure to perform an actual flash.
