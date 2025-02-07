package database

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/PlakarKorp/plakar/objects"
	"github.com/PlakarKorp/plakar/storage"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/lib"
)

func TestDatabaseBackend(t *testing.T) {
	t.Cleanup(func() {
		os.Remove("/tmp/testdb.db")
	})
	// create a repository
	repo := NewRepository("sqlite:///tmp/testdb.db")
	if repo == nil {
		t.Fatal("error creating repository")
	}

	location := repo.Location()
	require.Equal(t, "sqlite:///tmp/testdb.db", location)

	config := storage.NewConfiguration()
	serializedConfig, err := config.ToBytes()
	require.NoError(t, err)

	err = repo.Create("sqlite:///tmp/testdb.db", serializedConfig)
	require.NoError(t, err)

	_, err = repo.Open("sqlite:///tmp/testdb.db")
	require.NoError(t, err)
	//	require.Equal(t, repo.Configuration().Version, versioning.FromString(storage.VERSION))

	err = repo.Close()
	require.NoError(t, err)

	// states
	checksum1 := objects.Checksum{0x10, 0x20}
	checksum2 := objects.Checksum{0x30, 0x40}
	err = repo.PutState(checksum1, bytes.NewReader([]byte("test1")))
	require.NoError(t, err)
	err = repo.PutState(checksum2, bytes.NewReader([]byte("test2")))
	require.NoError(t, err)

	states, err := repo.GetStates()
	require.NoError(t, err)
	expected := []objects.Checksum{
		{0x10, 0x20, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
		{0x30, 0x40, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
	}
	require.Equal(t, expected, states)

	rd, err := repo.GetState(checksum2)
	require.NoError(t, err)
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, rd)
	require.NoError(t, err)
	require.Equal(t, "test2", buf.String())

	err = repo.DeleteState(checksum1)
	require.NoError(t, err)

	states, err = repo.GetStates()
	require.NoError(t, err)
	expected = []objects.Checksum{{0x30, 0x40, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}}
	require.Equal(t, expected, states)

	// packfiles
	checksum3 := objects.Checksum{0x50, 0x60}
	checksum4 := objects.Checksum{0x60, 0x70}
	err = repo.PutPackfile(checksum3, bytes.NewReader([]byte("test3")))
	require.NoError(t, err)
	err = repo.PutPackfile(checksum4, bytes.NewReader([]byte("test4")))
	require.NoError(t, err)

	packfiles, err := repo.GetPackfiles()
	require.NoError(t, err)
	expected = []objects.Checksum{
		{0x50, 0x60, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
		{0x60, 0x70, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
	}
	require.Equal(t, expected, packfiles)

	rd, err = repo.GetPackfileBlob(checksum4, 0, 4)
	buf = new(bytes.Buffer)
	_, err = io.Copy(buf, rd)
	require.NoError(t, err)
	require.Equal(t, "test", buf.String())

	err = repo.DeletePackfile(checksum3)
	require.NoError(t, err)

	packfiles, err = repo.GetPackfiles()
	require.NoError(t, err)
	expected = []objects.Checksum{{0x60, 0x70, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}}
	require.Equal(t, expected, packfiles)

	rd, err = repo.GetPackfile(checksum4)
	buf = new(bytes.Buffer)
	_, err = io.Copy(buf, rd)
	require.NoError(t, err)
	require.Equal(t, "test4", buf.String())
}
