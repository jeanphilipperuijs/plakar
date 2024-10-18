package header

import (
	"time"

	"github.com/PlakarLabs/plakar/logger"
	"github.com/PlakarLabs/plakar/profiler"
	"github.com/PlakarLabs/plakar/storage"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

type Blob struct {
	Type     string
	Version  string
	Checksum [32]byte
	Size     uint64
}

type Header struct {
	IndexID          uuid.UUID
	Version          string
	CreationTime     time.Time
	CreationDuration time.Duration
	PublicKey        string
	Tags             []string

	Hostname        string
	Username        string
	OperatingSystem string
	Architecture    string
	NumCPU          int
	MachineID       string
	ProcessID       int
	Client          string
	CommandLine     string

	ScanSize          uint64
	ScanProcessedSize uint64

	Root       [32]byte
	Metadata   [32]byte
	Statistics [32]byte

	ScannedDirectories []string

	FilesCount       uint64
	DirectoriesCount uint64

	FileKind      map[string]uint64
	FileType      map[string]uint64
	FileExtension map[string]uint64

	FilePercentKind      map[string]float64
	FilePercentType      map[string]float64
	FilePercentExtension map[string]float64
}

func NewHeader(indexID uuid.UUID) *Header {
	return &Header{
		IndexID:      indexID,
		CreationTime: time.Now(),
		Version:      storage.VERSION,
		Hostname:     "",
		Username:     "",
		CommandLine:  "",
		MachineID:    "",
		PublicKey:    "",

		Root: [32]byte{},

		FileKind:      make(map[string]uint64),
		FileType:      make(map[string]uint64),
		FileExtension: make(map[string]uint64),

		FilePercentKind:      make(map[string]float64),
		FilePercentType:      make(map[string]float64),
		FilePercentExtension: make(map[string]float64),
	}
}

func NewFromBytes(serialized []byte) (*Header, error) {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("header.NewIndexFromBytes", time.Since(t0))
		logger.Trace("header", "NewMetadataFromBytes(...): %s", time.Since(t0))
	}()

	var header Header
	if err := msgpack.Unmarshal(serialized, &header); err != nil {
		return nil, err
	} else {
		return &header, nil
	}
}

func (h *Header) Serialize() ([]byte, error) {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("header.Serialize", time.Since(t0))
		logger.Trace("header", "Serialize(): %s", time.Since(t0))
	}()

	if serialized, err := msgpack.Marshal(h); err != nil {
		return nil, err
	} else {
		return serialized, nil
	}
}

func (h *Header) GetIndexID() uuid.UUID {
	return h.IndexID
}

func (h *Header) GetIndexShortID() string {
	return h.IndexID.String()[:8]
}

func (h *Header) GetRoot() [32]byte {
	return h.Root
}
