package core

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"flasher/internal/platform"
)

type CompatibilityState string

const (
	CompatibilityVerified       CompatibilityState = "VERIFIED"
	CompatibilityCandidateMatch CompatibilityState = "CANDIDATE_MATCH"
	CompatibilityIncompatible   CompatibilityState = "INCOMPATIBLE"
	CompatibilityUnknown        CompatibilityState = "UNKNOWN"
)

type CompatibilityEvidence struct {
	Claim  string
	Source string
	Status string
}

type CompatibilityReport struct {
	State                  CompatibilityState
	TargetDevice           string
	TargetManufacturer     string
	TargetModel            string
	PackageDeviceIDs       []string
	FirmwareRequirements   []string
	RequiredPartitions     []string
	InstallationMethods    []string
	Evidence               []CompatibilityEvidence
	MissingRequirements    []string
	Conflicts              []string
	Reasons                []string
	Allowed                bool
	AcknowledgementAllowed bool
}

type CompatibilityEvidenceSet struct {
	ProfileVerified               bool
	ProfileSource                 string
	TrustedDeviceIDs              []string
	PackageTargetsVerified        bool
	PackageTargetSource           string
	FirmwareProfileVerified       bool
	PartitionProfileVerified      bool
	SlotProfileVerified           bool
	BootloaderProfileVerified     bool
	InstallationProfileVerified   bool
	FirmwareRequirementsComplete  bool
	TrustedFirmwareRequirements   []string
	PartitionRequirementsComplete bool
	TrustedRequiredPartitions     []string
	InstallationMethodsComplete   bool
	TrustedInstallationMethods    []string
	TrustedPackageDeviceIDs       []string
	PackageRequirementsSource     string
	InstalledFirmware             map[string]string
	ObservedPartitions            []string
}

type CompatibilityEvaluator interface {
	Evaluate(device platform.DeviceState, path, method string) CompatibilityReport
}

type CompatibilityEvaluatorFunc func(device platform.DeviceState, path, method string) CompatibilityReport

func (f CompatibilityEvaluatorFunc) Evaluate(device platform.DeviceState, path, method string) CompatibilityReport {
	return f(device, path, method)
}

func (r *CompatibilityReport) Error() string {
	if r == nil {
		return "<nil>"
	}
	if r.Allowed {
		return fmt.Sprintf("ROM compatibility %s for %s", r.State, r.TargetDevice)
	}
	details := append([]string(nil), r.Reasons...)
	details = append(details, r.MissingRequirements...)
	details = append(details, r.Conflicts...)
	return fmt.Sprintf("ROM compatibility %s for %s: %s",
		r.State, r.TargetDevice, strings.Join(details, "; "))
}

func (r CompatibilityReport) String() string { return (&r).Error() }

const (
	maxCompatibilityEntries       = 10000
	maxCompatibilityMetadataBytes = 2 << 20
	maxCompatibilityTotalBytes    = 8 << 20
)

var updaterDeviceAssertion = regexp.MustCompile(`(?i)assert\s*\([^;]*?getprop\s*\(\s*["']ro\.product\.(?:device|name|model)["']\s*\)\s*==\s*["']([^"']+)["']`)

type packageCompatibilityMetadata struct {
	DeviceIDs            []string `json:"device_ids"`
	FirmwareRequirements []string `json:"firmware_requirements"`
	RequiredPartitions   []string `json:"required_partitions"`
	InstallationMethods  []string `json:"installation_methods"`
}

type compatibilityPackage struct {
	kind                     string
	archiveEntries           int
	archiveCompressedBytes   uint64
	archiveUncompressedBytes uint64
	deviceIDs                []string
	firmwareRequirements     []string
	requiredPartitions       []string
	partitionNames           []string
	installationMethods      []string
	payload                  *PayloadInspection
	evidence                 []CompatibilityEvidence
	errors                   []string
	conflicts                []string
	targetGroups             [][]string
}

func EvaluateROMCompatibility(device platform.DeviceState, path string, method string) CompatibilityReport {
	return EvaluateROMCompatibilityWithEvidence(device, path, method, CompatibilityEvidenceSet{})
}

func EvaluateROMCompatibilityWithEvidence(device platform.DeviceState, path, method string, available CompatibilityEvidenceSet) CompatibilityReport {
	report := CompatibilityReport{
		State: CompatibilityUnknown, TargetDevice: device.Codename,
		TargetManufacturer: device.Manufacturer, TargetModel: device.Model, Reasons: []string{},
		MissingRequirements: []string{}, Conflicts: []string{}, Evidence: []CompatibilityEvidence{},
	}

	pkg, err := inspectCompatibilityPackage(path)
	if err != nil {
		report.Reasons = append(report.Reasons, err.Error())
		return report
	}
	report.PackageDeviceIDs = pkg.deviceIDs
	report.FirmwareRequirements = pkg.firmwareRequirements
	report.RequiredPartitions = pkg.requiredPartitions
	report.InstallationMethods = pkg.installationMethods
	report.Evidence = append(report.Evidence, pkg.evidence...)
	for _, parseError := range pkg.errors {
		report.Reasons = append(report.Reasons, parseError)
	}
	report.Conflicts = append(report.Conflicts, pkg.conflicts...)
	profileVerified := available.ProfileVerified &&
		strings.TrimSpace(available.ProfileSource) != "" &&
		hasIdentifier(available.TrustedDeviceIDs, device.Codename)
	if profileVerified {
		report.Evidence = append(report.Evidence, CompatibilityEvidence{
			Claim: "device profile", Source: available.ProfileSource, Status: "independently verified",
		})
	} else {
		report.MissingRequirements = append(report.MissingRequirements, "independently verified device profile")
	}
	for _, evidence := range []struct {
		claim    string
		verified bool
	}{
		{"firmware profile", available.FirmwareProfileVerified},
		{"partition profile", available.PartitionProfileVerified},
		{"slot profile", available.SlotProfileVerified},
		{"bootloader profile", available.BootloaderProfileVerified},
		{"installation profile", available.InstallationProfileVerified},
	} {
		if profileVerified && evidence.verified {
			report.Evidence = append(report.Evidence, CompatibilityEvidence{
				Claim: evidence.claim, Source: available.ProfileSource, Status: "independently verified",
			})
		} else {
			report.MissingRequirements = append(report.MissingRequirements, evidence.claim)
		}
	}
	packageTargetVerified := available.PackageTargetsVerified &&
		strings.TrimSpace(available.PackageTargetSource) != ""
	if packageTargetVerified {
		report.Evidence = append(report.Evidence, CompatibilityEvidence{
			Claim:  "package target declaration independently corroborated",
			Source: available.PackageTargetSource, Status: "independently verified",
		})
		if len(available.TrustedPackageDeviceIDs) == 0 ||
			!sameIdentifiers(pkg.deviceIDs, available.TrustedPackageDeviceIDs) {
			report.Conflicts = append(report.Conflicts, "package target metadata conflicts with independent package evidence")
		}
		if profileVerified && !hasIdentifierIntersection(available.TrustedPackageDeviceIDs, available.TrustedDeviceIDs) {
			report.Conflicts = append(report.Conflicts, "independent package target evidence does not match the selected device profile")
		}
	} else {
		report.MissingRequirements = append(report.MissingRequirements, "independent evidence confirming the package target declaration")
	}
	if len(pkg.deviceIDs) == 0 {
		report.MissingRequirements = append(report.MissingRequirements, "package-declared target device identifier")
	} else if strings.TrimSpace(device.Codename) != "" &&
		len(pkg.deviceIDs) > 0 && !hasIdentifier(pkg.deviceIDs, device.Codename) {
		report.Conflicts = append(report.Conflicts, "package target identifier does not match the observed device codename")
	}
	if hasIdentifier(pkg.deviceIDs, device.Codename) {
		report.State = CompatibilityCandidateMatch
		report.Reasons = append(report.Reasons, "package declaration matches the observed codename, but compatibility is not independently verified")
	}
	requirementsSourcePresent := strings.TrimSpace(available.PackageRequirementsSource) != ""
	if !available.FirmwareRequirementsComplete || !requirementsSourcePresent {
		report.MissingRequirements = append(report.MissingRequirements, "firmware requirements and matching installed-firmware evidence")
	} else if len(pkg.firmwareRequirements) == 0 || len(available.TrustedFirmwareRequirements) == 0 {
		report.MissingRequirements = append(report.MissingRequirements, "explicit firmware requirement evidence; an empty list is not proof of no requirement")
	} else {
		if !sameIdentifiers(pkg.firmwareRequirements, available.TrustedFirmwareRequirements) {
			report.Conflicts = append(report.Conflicts, "package firmware requirements conflict with independent requirements evidence")
		}
		report.Evidence = append(report.Evidence, CompatibilityEvidence{
			Claim: "firmware requirements", Source: available.PackageRequirementsSource, Status: "independently verified",
		})
		for _, requirement := range available.TrustedFirmwareRequirements {
			name, expected, ok := strings.Cut(requirement, "=")
			actual, observed := lookupFirmware(available.InstalledFirmware, name)
			if !ok || strings.TrimSpace(expected) == "" || !observed ||
				!strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(expected)) {
				report.MissingRequirements = append(report.MissingRequirements,
					"matching installed firmware for "+requirement)
			}
		}
	}
	if !available.PartitionRequirementsComplete || !requirementsSourcePresent {
		report.MissingRequirements = append(report.MissingRequirements, "required partition declaration")
	} else if len(pkg.requiredPartitions) == 0 || len(available.TrustedRequiredPartitions) == 0 {
		report.MissingRequirements = append(report.MissingRequirements, "explicit required-partition evidence; an empty list is not proof of no requirement")
	} else {
		if !sameIdentifiers(pkg.requiredPartitions, available.TrustedRequiredPartitions) {
			report.Conflicts = append(report.Conflicts, "package partition requirements conflict with independent requirements evidence")
		}
		report.Evidence = append(report.Evidence, CompatibilityEvidence{
			Claim: "required partitions", Source: available.PackageRequirementsSource, Status: "independently verified",
		})
		for _, partition := range available.TrustedRequiredPartitions {
			if !hasIdentifier(available.ObservedPartitions, partition) {
				report.MissingRequirements = append(report.MissingRequirements,
					"observed required partition "+partition)
			}
		}
	}
	if !available.InstallationMethodsComplete || !requirementsSourcePresent {
		report.MissingRequirements = append(report.MissingRequirements, "package-supported installation method")
	} else if len(pkg.installationMethods) == 0 || len(available.TrustedInstallationMethods) == 0 {
		report.MissingRequirements = append(report.MissingRequirements, "explicit supported installation-method evidence")
	} else if !sameIdentifiers(pkg.installationMethods, available.TrustedInstallationMethods) {
		report.Conflicts = append(report.Conflicts, "package installation methods conflict with independent requirements evidence")
	} else if !hasIdentifier(available.TrustedInstallationMethods, method) {
		report.Conflicts = append(report.Conflicts, "package does not declare the requested installation method")
	} else {
		report.Evidence = append(report.Evidence, CompatibilityEvidence{
			Claim: "supported installation methods", Source: available.PackageRequirementsSource, Status: "independently verified",
		})
	}
	if len(pkg.conflicts) > 0 || len(report.Conflicts) > 0 {
		report.State = CompatibilityIncompatible
	} else if len(pkg.errors) > 0 {
		report.State = CompatibilityUnknown
	}
	if len(report.MissingRequirements) == 0 && report.State != CompatibilityIncompatible &&
		report.State != CompatibilityUnknown &&
		profileVerified && len(pkg.deviceIDs) > 0 &&
		len(pkg.errors) == 0 && len(pkg.conflicts) == 0 && len(report.Conflicts) == 0 {
		report.State = CompatibilityVerified
		report.Allowed = true
		return report
	}
	if report.State != CompatibilityIncompatible && report.State != CompatibilityUnknown {
		report.State = CompatibilityCandidateMatch
	}
	report.AcknowledgementAllowed = len(pkg.errors) == 0 && len(pkg.conflicts) == 0 &&
		len(report.Conflicts) == 0 &&
		(report.State == CompatibilityCandidateMatch || report.State == CompatibilityUnknown)
	report.Reasons = append(report.Reasons, "compatibility evidence is incomplete")
	return report
}

func inspectCompatibilityPackage(path string) (compatibilityPackage, error) {
	file, err := os.Open(path)
	if err != nil {
		return compatibilityPackage{}, fmt.Errorf("open package: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return compatibilityPackage{}, fmt.Errorf("inspect package: %w", err)
	}
	return inspectCompatibilityPackageFile(file, path, info)
}

func inspectCompatibilityPackageFile(file *os.File, path string, info os.FileInfo) (compatibilityPackage, error) {
	if !info.Mode().IsRegular() {
		return compatibilityPackage{}, fmt.Errorf("package is not a regular file")
	}
	if info.Size() <= 0 || uint64(info.Size()) > maxROMInspectionFileSize {
		return compatibilityPackage{}, fmt.Errorf("package size is invalid or exceeds %d bytes", maxROMInspectionFileSize)
	}

	if err := validateZIPDirectoryLimits(file, info.Size()); err != nil {
		return compatibilityPackage{}, err
	}
	zipReader, zipErr := zip.NewReader(file, info.Size())
	if zipErr == nil {
		return inspectCompatibilityZIP(zipReader)
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		return compatibilityPackage{}, fmt.Errorf("malformed ROM ZIP: %w", zipErr)
	}
	header := make([]byte, 44)
	n, readErr := io.ReadFull(file, header)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return compatibilityPackage{}, fmt.Errorf("read image header: %w", readErr)
	}
	if n == len(header) && string(header[:8]) == "ANDROID!" &&
		binary.LittleEndian.Uint32(header[40:44]) <= 4 && info.Size() > int64(len(header)) {
		return compatibilityPackage{kind: "android-boot-image"}, nil
	}
	return compatibilityPackage{}, fmt.Errorf("unsupported package format; raw image has no reliable target metadata")
}

func inspectCompatibilityZIP(reader *zip.Reader) (compatibilityPackage, error) {
	pkg := compatibilityPackage{kind: "zip"}
	if len(reader.File) == 0 || len(reader.File) > maxCompatibilityEntries {
		return pkg, fmt.Errorf("ROM ZIP entry count is empty or exceeds %d", maxCompatibilityEntries)
	}
	pkg.archiveEntries = len(reader.File)
	var total uint64
	payloadFound := false
	for _, entry := range reader.File {
		pkg.archiveCompressedBytes = addArchiveSize(pkg.archiveCompressedBytes, entry.CompressedSize64)
		pkg.archiveUncompressedBytes = addArchiveSize(pkg.archiveUncompressedBytes, entry.UncompressedSize64)
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.HasPrefix(name, "/") || hasUnsafeArchivePath(name) || entry.Mode()&os.ModeSymlink != 0 {
			return pkg, fmt.Errorf("ROM ZIP contains unsafe archive path %q", entry.Name)
		}
		if strings.EqualFold(filepath.Ext(name), ".img") {
			pkg.partitionNames = append(pkg.partitionNames, strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
		}
		if strings.EqualFold(name, "payload.bin") {
			if payloadFound {
				pkg.errors = append(pkg.errors, "ROM ZIP contains multiple payload.bin entries")
				continue
			}
			payloadFound = true
			payload, err := inspectPayloadEntry(entry)
			if err != nil {
				pkg.errors = append(pkg.errors, err.Error())
				continue
			}
			pkg.payload = payload
			pkg.partitionNames = append(pkg.partitionNames, payload.Partitions...)
			continue
		}
		if entry.UncompressedSize64 > maxCompatibilityMetadataBytes {
			if isCompatibilityMetadata(name) {
				return pkg, fmt.Errorf("ROM ZIP compatibility metadata exceeds size limit")
			}
			continue
		}
		if !isCompatibilityMetadata(name) {
			continue
		}
		total += entry.UncompressedSize64
		if total > maxCompatibilityTotalBytes {
			return pkg, fmt.Errorf("ROM ZIP compatibility metadata exceeds total size limit")
		}
		content, err := readZipEntryBounded(entry, maxCompatibilityMetadataBytes)
		if err != nil {
			return pkg, fmt.Errorf("read ROM metadata %q: %w", name, err)
		}
		switch strings.ToLower(name) {
		case "meta-inf/com/android/metadata":
			targets := parseOTATargets(string(content))
			pkg.deviceIDs = append(pkg.deviceIDs, targets...)
			if len(targets) > 0 {
				pkg.targetGroups = append(pkg.targetGroups, normalizeIdentifiers(targets))
			}
			if len(pkg.deviceIDs) > 0 {
				pkg.evidence = append(pkg.evidence, CompatibilityEvidence{
					Claim: "OTA metadata declares target device", Source: name, Status: "package-declared; untrusted",
				})
			}
		case "meta-inf/com/google/android/updater-script":
			var targets []string
			for _, match := range updaterDeviceAssertion.FindAllStringSubmatch(string(content), -1) {
				if len(match) > 1 {
					targets = append(targets, strings.FieldsFunc(match[1], func(r rune) bool {
						return r == '|' || r == ','
					})...)
				}
			}
			pkg.deviceIDs = append(pkg.deviceIDs, targets...)
			if len(targets) > 0 {
				pkg.targetGroups = append(pkg.targetGroups, normalizeIdentifiers(targets))
			}
			if len(pkg.deviceIDs) > 0 {
				pkg.evidence = append(pkg.evidence, CompatibilityEvidence{
					Claim: "installer asserts device identifier", Source: name, Status: "package-declared; untrusted",
				})
			}
		case "meta-inf/flasher/compatibility.json":
			var metadata packageCompatibilityMetadata
			if err := json.Unmarshal(content, &metadata); err != nil {
				pkg.errors = append(pkg.errors, "malformed Flasher compatibility metadata")
				continue
			}
			pkg.deviceIDs = append(pkg.deviceIDs, metadata.DeviceIDs...)
			pkg.firmwareRequirements = append(pkg.firmwareRequirements, metadata.FirmwareRequirements...)
			pkg.requiredPartitions = append(pkg.requiredPartitions, metadata.RequiredPartitions...)
			pkg.partitionNames = append(pkg.partitionNames, metadata.RequiredPartitions...)
			pkg.installationMethods = append(pkg.installationMethods, metadata.InstallationMethods...)
			if len(metadata.DeviceIDs) > 0 {
				pkg.targetGroups = append(pkg.targetGroups, normalizeIdentifiers(metadata.DeviceIDs))
			}
			pkg.evidence = append(pkg.evidence, CompatibilityEvidence{
				Claim: "compatibility requirements", Source: name, Status: "package-declared; untrusted",
			})
		}
	}
	pkg.deviceIDs = normalizeIdentifiers(pkg.deviceIDs)
	pkg.firmwareRequirements = normalizeIdentifiers(pkg.firmwareRequirements)
	pkg.requiredPartitions = normalizeIdentifiers(pkg.requiredPartitions)
	pkg.partitionNames = normalizeIdentifiers(pkg.partitionNames)
	pkg.installationMethods = normalizeIdentifiers(pkg.installationMethods)
	for i := 0; i < len(pkg.targetGroups); i++ {
		for j := i + 1; j < len(pkg.targetGroups); j++ {
			if !hasIdentifierIntersection(pkg.targetGroups[i], pkg.targetGroups[j]) {
				pkg.conflicts = append(pkg.conflicts, "ROM ZIP contains conflicting target declarations")
				i = len(pkg.targetGroups)
				break
			}
		}
	}
	return pkg, nil
}

func isCompatibilityMetadata(name string) bool {
	switch strings.ToLower(name) {
	case "meta-inf/com/android/metadata",
		"meta-inf/com/google/android/updater-script",
		"meta-inf/flasher/compatibility.json":
		return true
	default:
		return false
	}
}

func readZipEntryBounded(entry *zip.File, limit uint64) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	content, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	closeErr := reader.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if uint64(len(content)) > limit {
		return nil, fmt.Errorf("entry exceeds %d byte limit", limit)
	}
	return content, nil
}

func hasUnsafeArchivePath(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) > 0 && strings.Contains(parts[0], ":") {
		return true
	}
	for _, part := range parts {
		if part == ".." || part == "." {
			return true
		}
	}
	return false
}

func parseOTATargets(metadata string) []string {
	var targets []string
	for _, line := range strings.Split(metadata, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "pre-device") {
			targets = append(targets, strings.FieldsFunc(value, func(r rune) bool {
				return r == ',' || r == '|'
			})...)
		}
	}
	return targets
}

func normalizeIdentifiers(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var result []string
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func hasIdentifier(values []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func hasIdentifierIntersection(left, right []string) bool {
	for _, value := range left {
		if hasIdentifier(right, value) {
			return true
		}
	}
	return false
}

func lookupFirmware(firmware map[string]string, name string) (string, bool) {
	name = strings.TrimSpace(name)
	for key, value := range firmware {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return value, true
		}
	}
	return "", false
}

func sameIdentifiers(left, right []string) bool {
	left = normalizeIdentifiers(left)
	right = normalizeIdentifiers(right)
	if len(left) != len(right) {
		return false
	}
	for _, value := range left {
		if !hasIdentifier(right, value) {
			return false
		}
	}
	return true
}
