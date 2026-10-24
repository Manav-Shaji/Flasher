package core

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	maxROMInspectionFileSize = uint64(32 << 30)
	maxPayloadManifestSize   = uint64(16 << 20)
	maxPayloadPartitions     = 4096
	maxZIPDirectorySize      = uint64(16 << 20)
	payloadHeaderSize        = 24
)

type ROMInspection struct {
	Kind                     string             `json:"kind"`
	ArchiveEntries           int                `json:"archive_entries"`
	ArchiveCompressedBytes   uint64             `json:"archive_compressed_bytes"`
	ArchiveUncompressedBytes uint64             `json:"archive_uncompressed_bytes"`
	DeviceIdentifiers        []string           `json:"device_identifiers"`
	PartitionNames           []string           `json:"partition_names"`
	RequiredPartitions       []string           `json:"required_partitions"`
	FirmwareRequirements     []string           `json:"firmware_requirements"`
	MetadataWarnings         []string           `json:"metadata_warnings"`
	MetadataConflicts        []string           `json:"metadata_conflicts"`
	Payload                  *PayloadInspection `json:"payload,omitempty"`
	Integrity                ROMIntegrityResult `json:"integrity"`
}

type PayloadInspection struct {
	Version                uint64   `json:"version"`
	ManifestBytes          uint64   `json:"manifest_bytes"`
	MetadataSignatureBytes uint32   `json:"metadata_signature_bytes"`
	BlockSize              uint64   `json:"block_size"`
	MinorVersion           uint64   `json:"minor_version"`
	PartialUpdate          bool     `json:"partial_update"`
	Partitions             []string `json:"partitions"`
}

type ROMIntegrityResult struct {
	Algorithm      string `json:"algorithm"`
	SHA256         string `json:"sha256"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	Status         string `json:"status"`
}

func InspectROM(path, expectedSHA256 string) (ROMInspection, error) {
	expectedSHA256 = strings.TrimSpace(expectedSHA256)
	if expectedSHA256 != "" {
		if len(expectedSHA256) != sha256.Size*2 {
			return ROMInspection{}, fmt.Errorf("invalid expected SHA-256 checksum length")
		}
		if _, err := hex.DecodeString(expectedSHA256); err != nil {
			return ROMInspection{}, fmt.Errorf("invalid expected SHA-256 checksum: %w", err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return ROMInspection{}, fmt.Errorf("open ROM package: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ROMInspection{}, fmt.Errorf("inspect ROM package: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ROMInspection{}, fmt.Errorf("ROM package is not a regular file")
	}
	if info.Size() <= 0 || uint64(info.Size()) > maxROMInspectionFileSize {
		return ROMInspection{}, fmt.Errorf("ROM package size must be between 1 byte and %d bytes", maxROMInspectionFileSize)
	}

	pkg, err := inspectCompatibilityPackageFile(file, path, info)
	if err != nil {
		return ROMInspection{}, err
	}
	result := ROMInspection{
		Kind: pkg.kind, ArchiveEntries: pkg.archiveEntries,
		ArchiveCompressedBytes:   pkg.archiveCompressedBytes,
		ArchiveUncompressedBytes: pkg.archiveUncompressedBytes,
		DeviceIdentifiers:        append([]string(nil), pkg.deviceIDs...),
		PartitionNames:           append([]string(nil), pkg.partitionNames...),
		RequiredPartitions:       append([]string(nil), pkg.requiredPartitions...),
		FirmwareRequirements:     append([]string(nil), pkg.firmwareRequirements...),
		MetadataWarnings:         append([]string(nil), pkg.errors...),
		MetadataConflicts:        append([]string(nil), pkg.conflicts...),
		Integrity:                ROMIntegrityResult{Algorithm: "SHA-256", ExpectedSHA256: expectedSHA256},
	}
	if pkg.payload != nil {
		payload := *pkg.payload
		payload.Partitions = append([]string(nil), pkg.payload.Partitions...)
		result.Payload = &payload
	}
	hash := sha256.New()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ROMInspection{}, fmt.Errorf("seek ROM package for SHA-256: %w", err)
	}
	bytesHashed, err := io.Copy(hash, file)
	if err != nil {
		return ROMInspection{}, fmt.Errorf("calculate ROM SHA-256: %w", err)
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return ROMInspection{}, fmt.Errorf("recheck ROM package after SHA-256: %w", err)
	}
	if bytesHashed != info.Size() || finalInfo.Size() != info.Size() ||
		!finalInfo.ModTime().Equal(info.ModTime()) {
		return ROMInspection{}, fmt.Errorf("ROM package changed during inspection")
	}
	result.Integrity.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if result.Integrity.ExpectedSHA256 == "" {
		result.Integrity.Status = "CALCULATED_ONLY"
		return result, nil
	}
	if strings.EqualFold(result.Integrity.SHA256, result.Integrity.ExpectedSHA256) {
		result.Integrity.Status = "EXPECTED_MATCH"
	} else {
		result.Integrity.Status = "EXPECTED_MISMATCH"
	}
	return result, nil
}

func validateZIPDirectoryLimits(file *os.File, size int64) error {
	const (
		endSignature        = uint32(0x06054b50)
		zip64EndSignature   = uint32(0x06064b50)
		zip64LocatorSig     = uint32(0x07064b50)
		endRecordSize       = 22
		maxArchiveComment   = 1<<16 - 1
		zip64LocatorSize    = 20
		zip64EndRecordFixed = 56
	)
	if size < endRecordSize {
		return nil
	}
	tailSize := int64(endRecordSize + maxArchiveComment)
	if size < tailSize {
		tailSize = size
	}
	tail := make([]byte, int(tailSize))
	if _, err := file.ReadAt(tail, size-tailSize); err != nil {
		return fmt.Errorf("read ZIP directory trailer: %w", err)
	}
	eocd := -1
	for i := len(tail) - endRecordSize; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:i+4]) != endSignature {
			continue
		}
		commentSize := int(binary.LittleEndian.Uint16(tail[i+20 : i+22]))
		if i+endRecordSize+commentSize == len(tail) {
			eocd = i
			break
		}
	}
	if eocd < 0 {
		return nil
	}

	endOffset := size - tailSize + int64(eocd)
	entries := uint64(binary.LittleEndian.Uint16(tail[eocd+10 : eocd+12]))
	directorySize := uint64(binary.LittleEndian.Uint32(tail[eocd+12 : eocd+16]))
	directoryOffset := uint64(binary.LittleEndian.Uint32(tail[eocd+16 : eocd+20]))
	zip64 := entries == math.MaxUint16 || directorySize == math.MaxUint32 ||
		directoryOffset == math.MaxUint32
	if zip64 {
		if endOffset < zip64LocatorSize {
			return fmt.Errorf("malformed ZIP64 locator")
		}
		locator := make([]byte, zip64LocatorSize)
		if _, err := file.ReadAt(locator, endOffset-zip64LocatorSize); err != nil {
			return fmt.Errorf("read ZIP64 locator: %w", err)
		}
		if binary.LittleEndian.Uint32(locator[:4]) != zip64LocatorSig {
			return fmt.Errorf("malformed ZIP64 locator")
		}
		recordOffset := binary.LittleEndian.Uint64(locator[8:16])
		if recordOffset > uint64(size) || uint64(size)-recordOffset < zip64EndRecordFixed {
			return fmt.Errorf("malformed ZIP64 end record offset")
		}
		record := make([]byte, zip64EndRecordFixed)
		if _, err := file.ReadAt(record, int64(recordOffset)); err != nil {
			return fmt.Errorf("read ZIP64 end record: %w", err)
		}
		if binary.LittleEndian.Uint32(record[:4]) != zip64EndSignature ||
			binary.LittleEndian.Uint64(record[4:12]) < zip64EndRecordFixed-12 {
			return fmt.Errorf("malformed ZIP64 end record")
		}
		entries = binary.LittleEndian.Uint64(record[32:40])
		directorySize = binary.LittleEndian.Uint64(record[40:48])
		directoryOffset = binary.LittleEndian.Uint64(record[48:56])
	}
	if entries > maxCompatibilityEntries {
		return fmt.Errorf("ROM ZIP entry count exceeds %d", maxCompatibilityEntries)
	}
	if directorySize > maxZIPDirectorySize {
		return fmt.Errorf("ROM ZIP central directory exceeds %d bytes", maxZIPDirectorySize)
	}
	if directoryOffset > math.MaxUint64-directorySize ||
		directoryOffset+directorySize > uint64(endOffset) {
		return fmt.Errorf("malformed ROM ZIP central directory bounds")
	}
	return nil
}

func inspectPayloadEntry(entry *zip.File) (*PayloadInspection, error) {
	if entry.UncompressedSize64 < payloadHeaderSize ||
		entry.UncompressedSize64 > maxROMInspectionFileSize {
		return nil, fmt.Errorf("payload.bin size is invalid or exceeds inspection limit")
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("open payload.bin: %w", err)
	}
	defer reader.Close()
	header := make([]byte, payloadHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, fmt.Errorf("read payload.bin header: %w", err)
	}
	if string(header[:4]) != "CrAU" {
		return nil, fmt.Errorf("payload.bin has invalid CrAU magic")
	}
	version := binary.BigEndian.Uint64(header[4:12])
	if version != 2 {
		return nil, fmt.Errorf("unsupported payload.bin major version %d", version)
	}
	manifestSize := binary.BigEndian.Uint64(header[12:20])
	signatureSize := binary.BigEndian.Uint32(header[20:24])
	metadataSize := uint64(payloadHeaderSize) + manifestSize + uint64(signatureSize)
	if manifestSize == 0 || manifestSize > maxPayloadManifestSize ||
		metadataSize < manifestSize || metadataSize > entry.UncompressedSize64 {
		return nil, fmt.Errorf("payload.bin manifest metadata size is invalid or exceeds inspection limit")
	}
	manifest := make([]byte, int(manifestSize))
	if _, err := io.ReadFull(reader, manifest); err != nil {
		return nil, fmt.Errorf("read payload.bin manifest: %w", err)
	}
	return parsePayloadManifest(version, manifestSize, signatureSize, manifest)
}

func parsePayloadManifest(version, manifestSize uint64, signatureSize uint32, data []byte) (*PayloadInspection, error) {
	result := &PayloadInspection{
		Version: version, ManifestBytes: manifestSize, MetadataSignatureBytes: signatureSize,
		BlockSize: 4096, Partitions: []string{},
	}
	offset := 0
	for offset < len(data) {
		field, wire, value, bytesValue, err := consumeProtoField(data, &offset)
		if err != nil {
			return nil, fmt.Errorf("malformed payload.bin manifest: %w", err)
		}
		switch field {
		case 3:
			if wire != 0 {
				return nil, fmt.Errorf("malformed payload.bin block size")
			}
			result.BlockSize = value
		case 12:
			if wire != 0 {
				return nil, fmt.Errorf("malformed payload.bin minor version")
			}
			result.MinorVersion = value
		case 13:
			if wire != 2 {
				return nil, fmt.Errorf("malformed payload.bin partition record")
			}
			if len(result.Partitions) >= maxPayloadPartitions {
				return nil, fmt.Errorf("payload.bin exceeds partition count limit")
			}
			name, err := parsePayloadPartition(bytesValue)
			if err != nil {
				return nil, err
			}
			if hasIdentifier(result.Partitions, name) {
				return nil, fmt.Errorf("payload.bin manifest contains duplicate partition %q", name)
			}
			if name != "" {
				result.Partitions = append(result.Partitions, name)
			}
		case 14:
			if wire != 0 {
				return nil, fmt.Errorf("malformed payload.bin partial-update flag")
			}
			result.PartialUpdate = value != 0
		}
	}
	if result.BlockSize == 0 {
		return nil, fmt.Errorf("payload.bin declares a zero block size")
	}
	result.Partitions = normalizeIdentifiers(result.Partitions)
	return result, nil
}

func parsePayloadPartition(data []byte) (string, error) {
	offset := 0
	name := ""
	for offset < len(data) {
		field, wire, _, bytesValue, err := consumeProtoField(data, &offset)
		if err != nil {
			return "", fmt.Errorf("malformed payload.bin partition record: %w", err)
		}
		if field == 1 {
			if wire != 2 || !utf8.Valid(bytesValue) || len(bytesValue) > 256 {
				return "", fmt.Errorf("malformed payload.bin partition name")
			}
			value := strings.TrimSpace(string(bytesValue))
			if value == "" || (name != "" && !strings.EqualFold(name, value)) {
				return "", fmt.Errorf("payload.bin partition name is empty")
			}
			name = value
		}
	}
	if name == "" {
		return "", fmt.Errorf("payload.bin partition record has no name")
	}
	return name, nil
}

func consumeProtoField(data []byte, offset *int) (int, int, uint64, []byte, error) {
	key, err := consumeProtoVarint(data, offset)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	fieldNumber := key >> 3
	if fieldNumber == 0 || fieldNumber > (1<<29)-1 {
		return 0, 0, 0, nil, fmt.Errorf("invalid protobuf field number")
	}
	field, wire := int(fieldNumber), int(key&7)
	switch wire {
	case 0:
		value, err := consumeProtoVarint(data, offset)
		return field, wire, value, nil, err
	case 1, 5:
		size := 8
		if wire == 5 {
			size = 4
		}
		if len(data)-*offset < size {
			return 0, 0, 0, nil, io.ErrUnexpectedEOF
		}
		*offset += size
		return field, wire, 0, nil, nil
	case 2:
		size, err := consumeProtoVarint(data, offset)
		if err != nil {
			return 0, 0, 0, nil, err
		}
		if size > uint64(len(data)-*offset) {
			return 0, 0, 0, nil, io.ErrUnexpectedEOF
		}
		start := *offset
		*offset += int(size)
		return field, wire, 0, data[start:*offset], nil
	default:
		return 0, 0, 0, nil, fmt.Errorf("unsupported wire type %d", wire)
	}
}

func consumeProtoVarint(data []byte, offset *int) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if *offset >= len(data) {
			return 0, io.ErrUnexpectedEOF
		}
		b := data[*offset]
		(*offset)++
		if shift == 63 && b > 1 {
			return 0, fmt.Errorf("varint overflow")
		}
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("varint overflow")
}

func addArchiveSize(total, size uint64) uint64 {
	if math.MaxUint64-total < size {
		return math.MaxUint64
	}
	return total + size
}
