# Flasher

Flasher is a production-grade, highly reliable Android flashing terminal utility. Built with Go, Bubble Tea, and strict safety validation at its core, it brings OEM-level safety checks into a modern cyberpunk-themed terminal UI.

## Features
- **Flash safety checks**: Flash operations validate device mode, explicit bootloader-unlocked status, battery state, and image accessibility before execution. Harmless recognized custom queries run without confirmation. In Fastboot mode, `reboot`, `reboot bootloader`, `reboot-bootloader`, `reboot fastboot`, `reboot recovery`, and `set_active a|b` are also allowed without a prompt, with a fresh selected-device check. Other custom writes remain blocked.
- **Flashing State Machine**: Enforces strict operational transitions ensuring no concurrent flash operations can corrupt your device. The TUI UI reacts dynamically to `FlashState` changes.
- **Cyberpunk UI**: Built on Charm's Bubble Tea, offering a beautifully styled dashboard with Device Info, Live Logs, and Action Menus.
- **Mode-aware device dashboards**: Automatically shows Android/ADB, Fastboot, or connection-attention diagnostics. Multiple connected devices require an explicit selection before device actions are available.
- **Device-bound operations**: Flashing, sideload, and reboot operations recheck and target the selected serial instead of relying on the platform tool's default device.
- **Reliable Executions**: All ADB and Fastboot commands run via managed interfaces with context timeouts, dynamic path resolution, and embedded binary support.
- **Audit Logging**: Generates persistent structured JSON logs for every operation to guarantee traceability for troubleshooting.

## Installation
Download the latest pre-compiled binaries for Windows, Linux, or macOS from the [Releases page](https://github.com/Manav-Shaji/RomFlasher/releases).

*(Note: Binaries can be embedded directly into the executable via the `internal/platform/bin` directory).*

## Build Instructions
1. Ensure you have Go 1.25.6+ installed.
2. Clone the repository: `git clone https://github.com/Manav-Shaji/RomFlasher.git`
3. Build the binary:
   ```sh
   go build -o Flasher.exe ./cmd/flasher
   ```

## Development
See the [Architecture Guide](docs/architecture.md) for project structure and safety boundaries. Validate source changes with `gofmt -l .`, `go build ./...`, and `go vet ./...`.

### Flashing Lifecycle
1. **Idle**: Waiting for command.
2. **Validating**: Checking battery, bootloader state, mode, compatibility, and image readability. The selected file's content fingerprint is calculated for change detection; an expected-checksum comparison is performed only when an expected value is supplied.
3. **Flashing**: Executing via ADB/Fastboot with context timeouts and retries.
4. **Completed / Failed**: Graceful shutdown and log serialization.

### Device Dashboard
Open the dashboard with `D` or the Dashboard menu action. It selects ADB or Fastboot panels from confirmed platform-tools discovery, and displays authorization/offline guidance when the connection needs attention. The Fastboot view is kept concise: device identity/connection, bootloader and security status, and selected key variables such as slot and anti-rollback information. It omits the full partition inventory and flash-preparation panel; the validated flash actions remain in the existing menu. Press `R` to rescan and refresh diagnostics. If more than one device is detected, use the arrow keys and Enter (or number keys 1-9) to select the intended target. Dashboard diagnostics never execute flash operations.

Flasher does not hardcode device compatibility profiles: it uses the identity reported by the connected ADB/Fastboot device, so the workflow is not limited to a built-in model list. The TUI supports Fastboot `.img` files for the **boot** and **recovery** partitions, `super_empty.img` with **Wipe Super**, and ROM `.zip` files through **ADB sideload**. ZIP metadata is inspected as untrusted data without extracting or running installer scripts. A detected package/device target conflict, malformed package, or conflicting metadata blocks the operation. When compatibility is unknown or only a candidate match, flashing, Wipe Super, and sideload require an explicit warning confirmation; this is not a `VERIFIED` result and can still leave the device unusable. Only proceed with a file independently confirmed for the exact device, model variant, partition, and installation method. Other image types, partitions, and ZIP installation methods are not currently provided by the TUI.

An optional ZIP entry `META-INF/flasher/compatibility.json` may declare `device_ids`, `firmware_requirements` (for example `["modem=1.2"]`), `required_partitions`, and `installation_methods` (such as `fastboot-flash` or `adb-sideload`). These declarations are untrusted package claims and are not sufficient by themselves to authorize flashing. ZIP parsing applies entry-count and metadata decompression limits and rejects unsafe paths; installer scripts are never executed. An adjacent `.sha256` file is not treated as an independently trusted reference. The CLI's `flash --sha256` option verifies against an expected value you obtain independently; a matching hash proves agreement with that value, not authenticity or compatibility. Existing flash actions continue to use the core device-safety validation and confirmation pipeline.

ADB sideload is designed for a simple file-picker workflow: select a ZIP, review the normal confirmation, and let Flasher run the selected-device `adb sideload` command. The preparation message shows the ZIP filename. Sideload does not calculate or verify SHA-256 and ignores adjacent checksum files; use the **Calculate SHA-256** menu action to calculate a digest and optionally compare it with a supplied 64-character SHA-256 value. The result reports the filename, full file path, digest, and whether the value matches. ROM metadata compatibility checks, protected staging, target checks, and the normal confirmation remain in place. Incomplete compatibility metadata or an unavailable Recovery codename shows a warning instead of blocking; a confirmed device-identifier mismatch still blocks. A known low or invalid battery reading still blocks, while an unavailable reading is disclosed in the confirmation and uses the normal confirmation rather than a separate typed phrase.

To retain the detected device codename for the matching-codename sideload confirmation, start Flasher while Android is connected, then use its Reboot Recovery action and select Apply update from ADB on the device. Flasher preserves only the identity associated with the same serial; if started after the device is already in sideload mode, its codename may be unavailable and the operation will be blocked with this guidance.

Use `flasher inspect <package.zip> [--sha256 <trusted-expected-hash>]` for a read-only JSON report containing ZIP sizes and entry count, package-declared device and firmware requirements, partition names, and supported `payload.bin` manifest metadata. SHA-256 is always reported as `CALCULATED_ONLY` unless an expected checksum was supplied; a match or mismatch is reported separately from compatibility and does not authenticate a package. Payload parsing reads only the bounded v2 manifest; it neither extracts partition data nor runs installer code. Unsupported or malformed payload metadata is surfaced as a warning and blocks compatibility authorization where required.

Before a CLI flash, use `flasher flash boot <image> --dry-run` (or substitute the exact partition). The dry run prints the selected Fastboot serial, exact operation and path, checksum status, compatibility status, and all blockers, without invoking any write/erase/flash/sideload command. Add `--sha256 <trusted-expected-hash>` to have the core validate an expected hash; a mismatch blocks execution. Actual CLI flashing requires `--yes`; unknown/candidate compatibility additionally requires `--allow-unverified-compatibility`, and the warning is printed before execution. `--force` only bypasses low/unknown battery checks (invalid readings still block) and does not bypass target, unlock, checksum, partition, compatibility-conflict, or acknowledgement checks. TUI flash, wipe-super, and sideload actions use explicit confirmation. Menu reboot actions run immediately, but recheck the selected device and use the core reboot validation/execution gate. Custom Fastboot also allows `reboot`, `reboot bootloader`, `reboot-bootloader`, `reboot fastboot`, `reboot recovery`, and `set_active a|b` without a prompt after a fresh selected-device check; other device-changing custom commands remain blocked. Generic engine commands and direct custom-command execution cannot bypass core authorization.

The file picker filters files by operation (`.zip` for ADB sideload and `.img` for Fastboot image actions) and automatically opens the selected device folder's `ROMs` subfolder for sideload or `Recovery` subfolder for Fastboot image actions. At startup, choose the parent ROM directory and then the device-specific folder (for example `Custom Rom\Devices\<device>`). If the expected subfolder is absent, the picker stays in the selected device folder; configured per-action folders take precedence.

The **Wipe Super** action selects a Fastboot `.img` file, normally `super_empty.img`, and invokes `fastboot -s <serial> wipe-super <image>`. It requires the usual target, compatibility, battery, and risk-confirmation checks. It is not a command to run casually: wiping `super` can leave the device unable to boot until a compatible ROM is installed.

No third-party dependency was added for OTA payload parsing. The reviewed `github.com/ssut/payload-dumper-go` API exposes useful manifest/partition information, but its current release requires Go 1.27 (above this project's Go 1.25.6 minimum), pulls a larger decompression/progress dependency graph, and allocates the manifest according to an untrusted header length without an application-level cap. The project therefore uses a bounded read-only manifest parser plus the standard `archive/zip` and `crypto/sha256` packages. This parser currently supports payload header major version 2 only.

See the [hardware validation procedure](docs/hardware-validation.md) for a
read-only device and ROM-inspection validation plan. Device-changing commands
must not be run as part of that procedure.
