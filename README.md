# Flasher

Flasher is a Windows, Linux, and macOS tool for common Android ADB and Fastboot tasks. It provides a terminal interface for selecting a connected device, checking its status, and choosing an image or ROM package.

## What it can do

- Flash `.img` files to **boot** or **recovery**.
- Use **Wipe Super** with a compatible `super_empty.img`.
- Install `.zip` packages through **ADB sideload** in Recovery.
- Show device information and calculate a file's SHA-256 hash.

Flasher uses the device identity reported by ADB or Fastboot; it does not limit the workflow to a built-in model list. This does not mean every ROM or image works on every device. Choose files specifically made for your exact device and intended partition. An unknown compatibility result requires extra confirmation and is not proof that a file is safe. A mismatch or malformed package is blocked.

## Important warnings

- **Back up first.** Unlocking the bootloader or flashing may erase your data.
- **Use files for your exact device and variant.** Similar model names do not guarantee compatibility. Verify the source and installation instructions before flashing.
- **Check the partition.** A boot image must be intended for `boot`; a recovery image must be intended for `recovery`. Do not use **Wipe Super** unless the image and recovery instructions specifically require a compatible `super_empty.img`.
- **Unknown compatibility is not a safety check.** Confirming an unverified warning does not make a file compatible. A wrong image can prevent the device from booting or cause permanent damage.
- **Keep the battery charged and the connection stable.** Do not unplug, power off, or interrupt a device during an operation.
- **Bootloader unlocking has risks.** It may wipe the device and can affect warranty or security; check your manufacturer's policy.

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
