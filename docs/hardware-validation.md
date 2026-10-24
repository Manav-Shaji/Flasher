# Hardware Validation Plan

This procedure verifies connection detection and diagnostic reporting without
flashing, erasing, unlocking, formatting, rebooting, or writing to a device.
Use a device and ROM package that you are authorized to test. Do not proceed
with an actual flash during this plan.

## Before testing

1. Build and vet the current source with `go build ./...` and
   `go vet ./...`.
2. Install current Android platform-tools and confirm `adb` and `fastboot` are
   available to the application.
3. Record the application version, platform-tools version, host OS, device
   model/codename, and test date.
4. Keep a separate terminal open for the reference commands below. Run only
   commands explicitly listed as read-only.
5. Do not include serial numbers, account details, tokens, or full build
   fingerprints in shared test reports unless necessary and approved.
6. For an intended CLI flash, use `flasher flash <partition> <image> --dry-run`.
   Confirm the report identifies the selected serial and shows checksum,
   compatibility, partition, and all blockers. This performs read-only
   discovery only; it does not send a flash command. Do not follow it with an
   actual flash as part of this validation plan.

## ROM package inspection

1. Inspect a representative authorized package with
   `flasher inspect <package.zip>`. This command is read-only and does not
   contact a device, extract partition images, or run installer scripts.
2. Review the JSON archive summary, declared identifiers, partition names,
   firmware metadata, payload manifest (when it is a supported v2 payload),
   and metadata warnings. Treat all package-declared values as untrusted.
3. If you have an expected SHA-256 from an independent trusted source, pass it
   with `--sha256 <expected>`. Confirm the result distinguishes
   `EXPECTED_MATCH`/`EXPECTED_MISMATCH` from `CALCULATED_ONLY`; none of those
   states alone establishes authenticity or device compatibility.
4. For non-device inspection, prepare harmless local fixtures containing
   malformed ZIPs, unsafe paths, oversized metadata, invalid payload
   headers/manifests, and incorrect checksums. Confirm the inspector reports
   errors or warnings without contacting a device or extracting/running
   package code. The inspector supports payload header major version 2 only;
   other versions should be reported as unsupported, not treated as compatible.

## Phase A: No device

1. Disconnect Android devices and exit Fastboot on any attached device using
   its normal on-device controls.
2. Launch Flasher and open the dashboard.
3. Verify the connection state is `DISCONNECTED`, that no device-specific
   readings remain, and that unavailable metrics are shown as unknown rather
   than zero or as a prior device's values.
4. Press `R` to refresh and verify that the UI remains responsive and stays
   disconnected.

## Phase B: ADB

1. Boot Android normally and connect one device over USB with USB debugging
   enabled.
2. In the reference terminal, run `adb devices -l`. If the device asks for
   authorization, verify Flasher reports the unauthorized state and does not
   show normal shell diagnostics. Approve the host only through the device's
   normal authorization prompt, then run `adb devices -l` again.
3. Open or refresh the dashboard. Compare the displayed product/model,
   manufacturer, codename, Android release, SDK, build ID, fingerprint, and
   security patch with:
   - `adb -s <serial> shell getprop`
   - `adb -s <serial> shell getprop ro.product.device`
4. Compare battery readings with `adb -s <serial> shell dumpsys battery`.
   Compare storage with `adb -s <serial> shell df /data`, memory with
   `adb -s <serial> shell cat /proc/meminfo`, and uptime with
   `adb -s <serial> shell cat /proc/uptime`.
5. Treat unavailable properties as unknown. Do not infer root absence from a
   failed `su` command, bootloader state from root, A/B support from an empty
   slot property, or ROM compatibility from the fingerprint.
6. Disconnect the device, refresh, and verify stale ADB readings clear. Reconnect
   it and verify diagnostics return for the same serial.
7. If supported in the test environment, repeat using an authorized ADB TCP/IP
   connection and confirm transport labeling is accurate.

## Phase C: Fastboot

1. Enter the device's Fastboot/bootloader mode using the manufacturer's
   supported physical-key or on-device procedure.
2. Run `fastboot devices` in the reference terminal and confirm Flasher selects
   Fastboot only after that tool reports the device.
3. Run the supported read-only query `fastboot -s <serial> getvar all` and
   compare the product, serial, slot, bootloader/security, battery, and
   allowlisted key-variable values shown by Flasher. The dashboard intentionally
   omits the complete variable dump and partition inventory. Some bootloaders
   print variables to stderr; this is expected and must not be mistaken for a
   write operation.
4. Verify unsupported variables and partition metadata remain unknown or
   unavailable. Do not assume a partition exists or has a particular size.
5. Return to Android using the device's normal supported procedure. Verify
   Fastboot diagnostics are cleared and ADB diagnostics return only after
   `adb devices -l` reports the device.

## Phase D: Multiple devices

1. Connect two supported devices, preferably one in Android and one in
   Fastboot.
2. Verify Flasher displays an ambiguous connection and requires explicit
   selection rather than silently choosing a device.
3. Select each candidate in turn. Compare its displayed serial, model, and
   readings with the corresponding per-device read-only reference commands.
4. Disconnect or change the mode of one device while diagnostics are running.
   Verify a delayed result for the previous selection does not replace the
   current device's dashboard.

## Phase E: ROM preparation

1. Select a known-good test image or ROM archive that is not intended to be
   flashed during this procedure. Record its source and expected device
   codename independently.
2. Verify the display distinguishes package presence/readability from a
   SHA-256 match, authenticity, device identity, ROM compatibility, firmware
   requirements, partition requirements, and backup readiness. Compatibility
   must be `VERIFIED` for a device-changing operation to pass core validation.
   The bundled F1/F6 profile aliases currently lack independent requirements
   evidence, so the default application is expected to block flashing.
3. For a checksum-match case, use a separately verified reference checksum.
   An adjacent `.sha256` file is not independently trusted by the app and must
   remain labeled `UNVERIFIED`, even when its value matches. The CLI accepts an
   independently sourced expected value with `--sha256`; a match establishes
   agreement with that value, not authenticity or device compatibility.
4. For a mismatch case, use a disposable copy and a deliberately incorrect
   sidecar. Confirm the package workflow reports a checksum mismatch and
   prevents proceeding through the selected-package flow.
5. For an incorrect-codename case, use only a test fixture or package metadata
   that is not submitted to a device. Confirm compatibility reports
   `INCOMPATIBLE` and core validation blocks; do not rely on a matching
   filename.
6. Remove or withhold firmware and partition evidence. Confirm those
   requirements remain unresolved and do not become verified from a codename
   match. The default application currently lacks an evidence source for these
   device-specific facts, and therefore does not authorize a flash.
7. Do not execute any operation plan or flash action in this phase.

## Phase F: Safety

1. Refresh dashboards in ADB and Fastboot modes while observing the reference
   terminal. Confirm refresh performs only read-only discovery and diagnostic
   queries; it must not reboot, flash, erase, format, unlock, sideload, or write.
2. In the custom-command UI, verify recognized read-only queries do not require
   a confirmation prompt and are sent to the currently selected serial.
3. Verify ordinary write, destructive, compound, shell-metacharacter, and
   unrecognized custom commands are rejected. The explicit Fastboot reboot
   requests and `set_active a|b` are allowed without a prompt after a fresh
   selected-device check; do not execute them on a validation device because
   they change device state.
4. Change or disconnect the selected device before a prepared operation. Verify
   the operation aborts when the target serial or mode no longer matches.
5. Review the operation planner and flash preflight using test files. Verify
   unsupported or incompatible operations remain blocked. Do not confirm or
   execute an actual flash, wipe, sideload, reboot, or unlock as part of this
   procedure.
6. A fresh scan only confirms current device detection/state. It cannot prove
   whether the interrupted transfer completed, that every partition is
   intact, or that any operation was rolled back. Do not retry or recover
   based solely on cancellation or successful reconnection; inspect the device
   and use an independently established, device-specific recovery procedure.

## Coverage and reporting

- The TUI binds confirmation to a calculated content fingerprint captured when
  the file is selected; CLI flash preflight similarly binds its plan to the
  preflight fingerprint. This is a content identity check, not an authenticity
  or publisher check, and is distinct from a caller-supplied `ExpectedSHA256`.
- Before flash, wipe-super, or sideload, core stages the exact fingerprint-
  matching bytes into a uniquely named temporary snapshot, evaluates
  compatibility and any expected checksum against that snapshot, and passes
  the snapshot path to platform-tools. It is staged in a private temporary
  directory (restrictive permissions where supported; Windows inherits the OS
  temporary-directory ACL) and explicitly deleted after the operation returns.
  The configured OS temporary directory needs free space for a full additional
  copy of the input (approximately its file size); a crash or forced process
  termination may leave a snapshot for manual cleanup.
- The checks in Phases A-F require physical devices, platform-tools, and
  representative package files.
- For each phase, record expected versus observed values, platform-tools
  versions, application build, device mode, and any discrepancy. Redact
  identifiers and firmware details before sharing logs.
- Begin with read-only diagnostics. Do not perform an actual flash until
  compatibility, package requirements, and the applicable device-specific
  procedure have been independently established.
