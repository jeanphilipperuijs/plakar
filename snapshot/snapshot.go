package snapshot

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PlakarLabs/plakar/logger"
	"github.com/PlakarLabs/plakar/objects"
	"github.com/PlakarLabs/plakar/packfile"
	"github.com/PlakarLabs/plakar/profiler"
	"github.com/PlakarLabs/plakar/repository"
	"github.com/PlakarLabs/plakar/repository/state"
	"github.com/PlakarLabs/plakar/snapshot/header"
	"github.com/PlakarLabs/plakar/snapshot/metadata"
	"github.com/PlakarLabs/plakar/snapshot/statistics"
	"github.com/PlakarLabs/plakar/snapshot/vfs"
	"github.com/google/uuid"
)

type Snapshot struct {
	repository *repository.Repository
	stateDelta *state.State

	filesystem *vfs.Filesystem

	SkipDirs []string

	Header *header.Header

	Metadata *metadata.Metadata

	statistics *statistics.Statistics

	packerChan     chan interface{}
	packerChanDone chan bool
}

type PackerMsg struct {
	Timestamp time.Time
	Type      uint8
	Checksum  [32]byte
	Data      []byte
}

func New(repo *repository.Repository, snapshotID [32]byte) (*Snapshot, error) {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.Create", time.Since(t0))
	}()

	snapshot := &Snapshot{
		repository: repo,
		stateDelta: state.New(),

		Header:   header.NewHeader(snapshotID),
		Metadata: metadata.New(),

		statistics: statistics.New(),

		packerChan:     make(chan interface{}, runtime.NumCPU()*2+1),
		packerChanDone: make(chan bool),
	}
	snapshot.stateDelta.Metadata.Extends = repo.State().Metadata.Extends

	go func() {
		wg := sync.WaitGroup{}
		for i := 0; i < runtime.NumCPU(); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var pack *packfile.PackFile
				var snapshotHeaders map[[32]byte]struct{}
				var chunks map[[32]byte]struct{}
				var objects map[[32]byte]struct{}
				var files map[[32]byte]struct{}
				var directories map[[32]byte]struct{}
				var datas map[[32]byte]struct{}

				for msg := range snapshot.packerChan {
					if pack == nil {
						pack = packfile.New()
						snapshotHeaders = make(map[[32]byte]struct{})
						chunks = make(map[[32]byte]struct{})
						objects = make(map[[32]byte]struct{})
						files = make(map[[32]byte]struct{})
						directories = make(map[[32]byte]struct{})
						datas = make(map[[32]byte]struct{})
					}

					if msg, ok := msg.(*PackerMsg); !ok {
						panic("received data with unexpected type")
					} else {
						logger.Trace("packer", "%s: PackerMsg(%d, %064x), dt=%s", snapshot.Header.GetIndexShortID(), msg.Type, msg.Checksum, time.Since(msg.Timestamp))
						pack.AddBlob(msg.Type, msg.Checksum, msg.Data)
						switch msg.Type {
						case packfile.TYPE_SNAPSHOT:
							snapshotHeaders[msg.Checksum] = struct{}{}
						case packfile.TYPE_CHUNK:
							chunks[msg.Checksum] = struct{}{}
						case packfile.TYPE_OBJECT:
							objects[msg.Checksum] = struct{}{}
						case packfile.TYPE_FILE:
							files[msg.Checksum] = struct{}{}
						case packfile.TYPE_DIRECTORY:
							directories[msg.Checksum] = struct{}{}
						case packfile.TYPE_DATA:
							datas[msg.Checksum] = struct{}{}
						default:
							panic("received msg with unexpected blob type")
						}
					}

					if pack.Size() > uint32(repo.Configuration().PackfileSize) {
						snapshotHeadersList := make([][32]byte, len(snapshotHeaders))
						for snapshotID := range snapshotHeaders {
							snapshotHeadersList = append(snapshotHeadersList, snapshotID)
						}
						objectsList := make([][32]byte, len(objects))
						for objectChecksum := range objects {
							objectsList = append(objectsList, objectChecksum)
						}
						chunksList := make([][32]byte, len(chunks))
						for chunkChecksum := range chunks {
							chunksList = append(chunksList, chunkChecksum)
						}
						filesList := make([][32]byte, len(files))
						for fileChecksum := range files {
							filesList = append(filesList, fileChecksum)
						}
						directoriesList := make([][32]byte, len(directories))
						for directoryChecksum := range directories {
							directoriesList = append(directoriesList, directoryChecksum)
						}
						datasList := make([][32]byte, len(datas))
						for dataChecksum := range datas {
							datasList = append(datasList, dataChecksum)
						}
						err := snapshot.PutPackfile(pack, objectsList, chunksList, filesList, directoriesList, datasList, snapshotHeadersList)
						if err != nil {
							panic(err)
						}
						pack = nil
					}
				}

				if pack != nil {
					snapshotHeadersList := make([][32]byte, len(snapshotHeaders))
					for snapshotID := range snapshotHeaders {
						snapshotHeadersList = append(snapshotHeadersList, snapshotID)
					}
					objectsList := make([][32]byte, len(objects))
					for objectChecksum := range objects {
						objectsList = append(objectsList, objectChecksum)
					}
					chunksList := make([][32]byte, len(chunks))
					for chunkChecksum := range chunks {
						chunksList = append(chunksList, chunkChecksum)
					}
					filesList := make([][32]byte, len(files))
					for fileChecksum := range files {
						filesList = append(filesList, fileChecksum)
					}
					directoriesList := make([][32]byte, len(directories))
					for fileChecksum := range directories {
						directoriesList = append(directoriesList, fileChecksum)
					}
					datasList := make([][32]byte, len(datas))
					for dataChecksum := range datas {
						datasList = append(datasList, dataChecksum)
					}
					err := snapshot.PutPackfile(pack, objectsList, chunksList, filesList, directoriesList, datasList, snapshotHeadersList)
					if err != nil {
						panic(err)
					}
					pack = nil
				}
			}()
		}
		wg.Wait()
		snapshot.packerChanDone <- true
		close(snapshot.packerChanDone)
	}()

	logger.Trace("snapshot", "%s: New()", snapshot.Header.GetIndexShortID())
	return snapshot, nil
}

func Load(repo *repository.Repository, snapshotID [32]byte) (*Snapshot, error) {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.Load", time.Since(t0))
	}()

	hdr, _, err := GetSnapshot(repo, snapshotID)
	if err != nil {
		return nil, err
	}

	snapshot := &Snapshot{}
	snapshot.repository = repo
	snapshot.Header = hdr

	logger.Trace("snapshot", "%x: Load()", snapshot.Header.GetIndexShortID())
	return snapshot, nil
}

func Fork(repo *repository.Repository, snapshotID [32]byte) (*Snapshot, error) {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.Fork", time.Since(t0))
	}()

	hdr, _, err := GetSnapshot(repo, snapshotID)
	if err != nil {
		return nil, err
	}

	snapshot := &Snapshot{
		repository: repo,

		Header: hdr,
	}

	indexID := uuid.Must(uuid.NewRandom())
	snapshot.Header.IndexID = repo.Checksum(indexID[:])

	logger.Trace("snapshot", "%s: Fork(): %s", indexID, snapshot.Header.GetIndexShortID())
	return snapshot, nil
}

func GetSnapshot(repo *repository.Repository, snapshotID [32]byte) (*header.Header, bool, error) {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.GetSnapshot", time.Since(t0))
	}()
	logger.Trace("snapshot", "repository.GetSnapshot(%x)", snapshotID)

	buffer, err := repo.GetSnapshot(snapshotID)
	if err != nil {
		return nil, false, err
	}

	hdr, err := header.NewFromBytes(buffer)
	if err != nil {
		return nil, false, err
	}

	return hdr, false, nil
}

func GetMetadata(repo *repository.Repository, checksum [32]byte) (*metadata.Metadata, [32]byte, error) {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.GetMetadata", time.Since(t0))
	}()

	packfile, offset, length, exists := repo.State().GetSubpartForData(checksum)
	if !exists {
		return nil, [32]byte{}, fmt.Errorf("metadata not found")
	}

	buffer, err := repo.GetPackfileBlob(packfile, offset, length)
	if err != nil {
		return nil, [32]byte{}, err
	}

	md, err := metadata.NewFromBytes(buffer)
	if err != nil {
		return nil, [32]byte{}, err
	}

	verifyChecksum := repo.Checksum(buffer)
	verifyChecksum32 := [32]byte{}
	copy(verifyChecksum32[:], verifyChecksum[:])

	return md, verifyChecksum32, nil
}

func (snap *Snapshot) PutHeader(checksum [32]byte, data []byte) error {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.PutHeader", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: PutHeader(%064x)", snap.Header.GetIndexShortID(), checksum)

	encoded, err := snap.repository.Encode(data)
	if err != nil {
		return err
	}

	snap.packerChan <- &PackerMsg{Type: packfile.TYPE_SNAPSHOT, Timestamp: time.Now(), Checksum: checksum, Data: encoded}
	return nil
}

func (snap *Snapshot) PutChunk(checksum [32]byte, data []byte) error {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.PutChunk", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: PutChunk(%064x)", snap.Header.GetIndexShortID(), checksum)

	encoded, err := snap.repository.Encode(data)
	if err != nil {
		return err
	}

	atomic.AddUint64(&snap.statistics.ChunksTransferCount, 1)
	atomic.AddUint64(&snap.statistics.ChunksTransferSize, uint64(len(data)))

	snap.packerChan <- &PackerMsg{Type: packfile.TYPE_CHUNK, Timestamp: time.Now(), Checksum: checksum, Data: encoded}
	return nil
}

func (snap *Snapshot) PutData(checksum [32]byte, data []byte) error {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.PutData", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: PutData(%064x)", snap.Header.GetIndexShortID(), checksum)

	encoded, err := snap.repository.Encode(data)
	if err != nil {
		return err
	}

	atomic.AddUint64(&snap.statistics.DataTransferCount, 1)
	atomic.AddUint64(&snap.statistics.DataTransferSize, uint64(len(encoded)))

	snap.packerChan <- &PackerMsg{Type: packfile.TYPE_DATA, Timestamp: time.Now(), Checksum: checksum, Data: encoded}
	return nil
}

func (snapshot *Snapshot) Repository() *repository.Repository {
	return snapshot.repository
}

func (snap *Snapshot) PutObject(checksum [32]byte, data []byte) error {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.PutObject", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: PutObject(%064x)", snap.Header.GetIndexShortID(), checksum)

	encoded, err := snap.repository.Encode(data)
	if err != nil {
		return err
	}
	atomic.AddUint64(&snap.statistics.ObjectsTransferCount, 1)
	atomic.AddUint64(&snap.statistics.ObjectsTransferSize, uint64(len(encoded)))

	snap.packerChan <- &PackerMsg{Type: packfile.TYPE_OBJECT, Timestamp: time.Now(), Checksum: checksum, Data: encoded}
	return nil
}

func (snap *Snapshot) PutFile(checksum [32]byte, data []byte) error {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.PutFile", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: PutFile(%064x)", snap.Header.GetIndexShortID(), checksum)

	encoded, err := snap.repository.Encode(data)
	if err != nil {
		return err
	}

	atomic.AddUint64(&snap.statistics.VFSFilesTransferCount, 1)
	atomic.AddUint64(&snap.statistics.VFSFilesTransferSize, uint64(len(encoded)))

	snap.packerChan <- &PackerMsg{Type: packfile.TYPE_FILE, Timestamp: time.Now(), Checksum: checksum, Data: encoded}
	return nil
}

func (snap *Snapshot) PutDirectory(checksum [32]byte, data []byte) error {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.PutDirectory", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: PutDirectory(%064x)", snap.Header.GetIndexShortID(), checksum)

	encoded, err := snap.repository.Encode(data)
	if err != nil {
		return err
	}

	atomic.AddUint64(&snap.statistics.VFSDirectoriesTransferCount, 1)
	atomic.AddUint64(&snap.statistics.VFSDirectoriesTransferSize, uint64(len(encoded)))

	snap.packerChan <- &PackerMsg{Type: packfile.TYPE_DIRECTORY, Timestamp: time.Now(), Checksum: checksum, Data: encoded}
	return nil
}

func (snap *Snapshot) PutPackfile(pack *packfile.PackFile, objects [][32]byte, chunks [][32]byte, files [][32]byte, directories [][32]byte, datas [][32]byte, snapshots [][32]byte) error {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.PutPackfile", time.Since(t0))
	}()

	repo := snap.repository

	serializedData, err := pack.SerializeData()
	if err != nil {
		panic("could not serialize pack file data" + err.Error())
	}
	serializedIndex, err := pack.SerializeIndex()
	if err != nil {
		panic("could not serialize pack file index" + err.Error())
	}
	serializedFooter, err := pack.SerializeFooter()
	if err != nil {
		panic("could not serialize pack file footer" + err.Error())
	}

	encryptedIndex, err := repo.Encode(serializedIndex)
	if err != nil {
		return err
	}

	encryptedFooter, err := repo.Encode(serializedFooter)
	if err != nil {
		return err
	}

	encryptedFooterLength := uint8(len(encryptedFooter))

	versionBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(versionBytes, pack.Footer.Version)

	serializedPackfile := append(serializedData, encryptedIndex...)
	serializedPackfile = append(serializedPackfile, encryptedFooter...)
	serializedPackfile = append(serializedPackfile, versionBytes...)
	serializedPackfile = append(serializedPackfile, byte(encryptedFooterLength))

	checksum := snap.repository.Checksum(serializedPackfile)

	var checksum32 [32]byte
	copy(checksum32[:], checksum[:])

	atomic.AddUint64(&snap.statistics.PackfilesCount, 1)
	atomic.AddUint64(&snap.statistics.PackfilesSize, uint64(len(serializedPackfile)))

	logger.Trace("snapshot", "%s: PutPackfile(%x, ...)", snap.Header.GetIndexShortID(), checksum32)
	err = snap.repository.PutPackfile(checksum32, serializedPackfile)
	if err != nil {
		panic("could not write pack file")
	}

	atomic.AddUint64(&snap.statistics.PackfilesTransferCount, 1)
	atomic.AddUint64(&snap.statistics.PackfilesTransferSize, uint64(len(serializedPackfile)))

	for _, chunkChecksum := range chunks {
		for idx, chunk := range pack.Index {
			if chunk.Checksum == chunkChecksum {
				snap.Repository().State().SetPackfileForChunk(checksum32,
					chunkChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				snap.stateDelta.SetPackfileForChunk(checksum32,
					chunkChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				break
			}
		}
	}

	for _, objectChecksum := range objects {
		for idx, chunk := range pack.Index {
			if chunk.Checksum == objectChecksum {
				snap.Repository().State().SetPackfileForObject(checksum32,
					objectChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				snap.stateDelta.SetPackfileForObject(checksum32,
					objectChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				break
			}
		}
	}

	for _, fileChecksum := range files {
		for idx, chunk := range pack.Index {
			if chunk.Checksum == fileChecksum {
				snap.Repository().State().SetPackfileForFile(checksum32,
					fileChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				snap.stateDelta.SetPackfileForFile(checksum32,
					fileChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				break
			}
		}
	}

	for _, directoryChecksum := range directories {
		for idx, chunk := range pack.Index {
			if chunk.Checksum == directoryChecksum {
				snap.Repository().State().SetPackfileForDirectory(checksum32,
					directoryChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				snap.stateDelta.SetPackfileForDirectory(checksum32,
					directoryChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				break
			}
		}
	}

	for _, dataChecksum := range datas {
		for idx, chunk := range pack.Index {
			if chunk.Checksum == dataChecksum {
				snap.Repository().State().SetPackfileForData(checksum32,
					dataChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				snap.stateDelta.SetPackfileForData(checksum32,
					dataChecksum,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				break
			}
		}
	}

	for _, snapshotID := range snapshots {
		for idx, chunk := range pack.Index {
			if chunk.Checksum == snapshotID {
				snap.Repository().State().SetPackfileForSnapshot(checksum32,
					snapshotID,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				snap.stateDelta.SetPackfileForSnapshot(checksum32,
					snapshotID,
					pack.Index[idx].Offset,
					pack.Index[idx].Length)
				break
			}
		}
	}
	return nil
}

func (snapshot *Snapshot) GetChunk(checksum [32]byte) ([]byte, error) {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.GetChunk", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: GetChunk(%064x)", snapshot.Header.GetIndexShortID(), checksum)

	packfileChecksum, offset, length, exists := snapshot.Repository().State().GetSubpartForChunk(checksum)
	if !exists {
		return nil, fmt.Errorf("packfile not found")
	}

	buffer, err := snapshot.repository.GetPackfileBlob(packfileChecksum, offset, length)
	if err != nil {
		return nil, err
	}
	return buffer, nil
}

func (snapshot *Snapshot) CheckFile(checksum [32]byte) bool {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.CheckFile", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: CheckFile(%064x)", snapshot.Header.GetIndexShortID(), checksum)

	return snapshot.Repository().State().FileExists(checksum)
}

func (snapshot *Snapshot) CheckDirectory(checksum [32]byte) bool {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.CheckDirectory", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: CheckDirectory(%064x)", snapshot.Header.GetIndexShortID(), checksum)

	return snapshot.Repository().State().DirectoryExists(checksum)
}

func (snapshot *Snapshot) CheckChunk(checksum [32]byte) bool {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.CheckChunk", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: CheckChunk(%064x)", snapshot.Header.GetIndexShortID(), checksum)

	return snapshot.Repository().State().ChunkExists(checksum)

}

func (snapshot *Snapshot) CheckObject(checksum [32]byte) bool {
	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.CheckObject", time.Since(t0))
	}()
	logger.Trace("snapshot", "%s: CheckObject(%064x)", snapshot.Header.GetIndexShortID(), checksum)

	return snapshot.Repository().State().ObjectExists(checksum)
}

func (snapshot *Snapshot) Commit() error {

	repo := snapshot.repository

	t0 := time.Now()
	defer func() {
		profiler.RecordEvent("snapshot.Commit", time.Since(t0))
	}()

	serializedHdr, err := snapshot.Header.Serialize()
	if err != nil {
		return err
	}

	if err := snapshot.PutHeader(snapshot.Header.IndexID, serializedHdr); err != nil {
		return err
	}

	close(snapshot.packerChan)
	<-snapshot.packerChanDone

	serializedRepositoryIndex, err := snapshot.stateDelta.Serialize()
	if err != nil {
		logger.Warn("could not serialize repository index: %s", err)
		return err
	}
	indexChecksum := snapshot.repository.Checksum(serializedRepositoryIndex)
	indexChecksum32 := [32]byte{}
	copy(indexChecksum32[:], indexChecksum[:])
	_, err = repo.PutState(indexChecksum32, serializedRepositoryIndex)
	if err != nil {
		return err
	}

	logger.Trace("snapshot", "%s: Commit()", snapshot.Header.GetIndexShortID())
	return nil
}

func (snapshot *Snapshot) NewReader(pathname string) (*Reader, error) {
	return NewReader(snapshot, pathname)
}

func (snapshot *Snapshot) LookupObject(checksum [32]byte) (*objects.Object, error) {
	packfile, offset, length, exists := snapshot.Repository().State().GetSubpartForObject(checksum)
	if !exists {
		return nil, fmt.Errorf("object not found")
	}

	buffer, err := snapshot.repository.GetPackfileBlob(packfile, offset, length)
	if err != nil {
		return nil, err
	}

	return objects.NewObjectFromBytes(buffer)
}
