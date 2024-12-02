package vfs

import (
	"path/filepath"
	"sort"

	"github.com/PlakarKorp/plakar/objects"
	"github.com/PlakarKorp/plakar/snapshot/importer"
	"github.com/vmihailenco/msgpack/v5"
)

type FileEntry struct {
	Version    uint32              `msgpack:"version" json:"version"`
	ParentPath string              `msgpack:"parent_path" json:"parent_path"`
	Type       importer.RecordType `msgpack:"type" json:"type"`
	FileInfo   objects.FileInfo    `msgpack:"file_info" json:"file_info"`

	/* File specific fields */
	SymlinkTarget string          `msgpack:"symlinkTarget,omitempty" json:"symlinkTarget,omitempty"`
	Object        *objects.Object `msgpack:"object,omitempty" json:"object,omitempty"` // nil for !regular files

	/* Windows specific fields */
	AlternateDataStreams []AlternateDataStream `msgpack:"alternate_data_streams,omitempty" json:"alternate_data_streams,omitempty"`
	SecurityDescriptor   []byte                `msgpack:"security_descriptor,omitempty" json:"security_descriptor,omitempty"`
	FileAttributes       uint32                `msgpack:"file_attributes,omitempty" json:"file_attributes,omitempty"`

	/* Unix fields */
	ExtendedAttributes []ExtendedAttribute `msgpack:"extended_attributes,omitempty" json:"extended_attributes,omitempty"`

	/* Custom metadata and tags */
	CustomMetadata []CustomMetadata `msgpack:"custom_metadata,omitempty" json:"custom_metadata,omitempty"`
	Tags           []string         `msgpack:"tags,omitempty" json:"tags,omitempty"`
}

func (*FileEntry) fsEntry() {}

func NewFileEntry(parentPath string, record *importer.ScanRecord) *FileEntry {
	target := ""
	if record.Target != "" {
		target = record.Target
	}

	ExtendedAttributes := make([]ExtendedAttribute, 0)
	for name, value := range record.ExtendedAttributes {
		ExtendedAttributes = append(ExtendedAttributes, ExtendedAttribute{
			Name:  name,
			Value: value,
		})
	}

	sort.Slice(ExtendedAttributes, func(i, j int) bool {
		return ExtendedAttributes[i].Name < ExtendedAttributes[j].Name
	})

	return &FileEntry{
		Version:            VERSION,
		Type:               record.Type,
		FileInfo:           record.FileInfo,
		SymlinkTarget:      target,
		ExtendedAttributes: ExtendedAttributes,
		Tags:               []string{},
		ParentPath:         parentPath,
	}
}

func FileEntryFromBytes(serialized []byte) (*FileEntry, error) {
	var f FileEntry
	if err := msgpack.Unmarshal(serialized, &f); err != nil {
		return nil, err
	}
	if f.Object != nil {
		if f.Object.CustomMetadata == nil {
			f.Object.CustomMetadata = make([]objects.CustomMetadata, 0)
		}
		if f.Object.Tags == nil {
			f.Object.Tags = make([]string, 0)
		}
	}
	if f.AlternateDataStreams == nil {
		f.AlternateDataStreams = make([]AlternateDataStream, 0)
	}
	if f.SecurityDescriptor == nil {
		f.SecurityDescriptor = make([]byte, 0)
	}
	if f.ExtendedAttributes == nil {
		f.ExtendedAttributes = make([]ExtendedAttribute, 0)
	}
	if f.CustomMetadata == nil {
		f.CustomMetadata = make([]CustomMetadata, 0)
	}
	if f.Tags == nil {
		f.Tags = make([]string, 0)
	}
	return &f, nil
}

func (f *FileEntry) Serialize() ([]byte, error) {
	data, err := msgpack.Marshal(f)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (f *FileEntry) Stat() *objects.FileInfo {
	return &f.FileInfo
}

func (f *FileEntry) Name() string {
	return f.Stat().Name()
}

func (f *FileEntry) Path() string {
	return filepath.Join(f.ParentPath, f.Name())
}

func (f *FileEntry) Size() int64 {
	return f.Stat().Size()
}

func (f *FileEntry) ContentType() string {
	if f.Object == nil {
		return ""
	}
	return f.Object.ContentType
}

func (f *FileEntry) Entropy() float64 {
	if f.Object == nil {
		return 0
	}
	return f.Object.Entropy
}
