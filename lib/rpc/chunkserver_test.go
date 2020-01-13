package rpc

import (
	"errors"
	"github.com/stretchr/testify/assert"
	"testing"
	"zircon/apis"
	"zircon/apis/mocks"
)

func beginChunkserverTest(t *testing.T) (*mocks.Chunkserver, func(), apis.Chunkserver) {
	cache := NewConnectionCache()
	mocked := new(mocks.Chunkserver)

	teardown, address, err := PublishChunkserver(mocked, ":0")
	assert.NoError(t, err)

	server, err := cache.SubscribeChunkserver(address)
	assert.NoError(t, err)

	return mocked, func() {
		mocked.AssertExpectations(t)

		teardown(true)
		cache.CloseAll()
	}, server
}

func TestChunkserver_StartWriteReplicated(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("StartWriteReplicated", apis.ChunkNum(73), uint32(55), []byte("this is a hello\000 world!!\n"),
		[]apis.ServerAddress{"abc", "def", "ghi.mit.edu"}).Return(nil)
	mocked.On("StartWriteReplicated", apis.ChunkNum(0), uint32(0), []byte("|||"),
		[]apis.ServerAddress{}).Return(errors.New("hello world 01"))

	err := server.StartWriteReplicated(73, 55, []byte("this is a hello\000 world!!\n"),
		[]apis.ServerAddress{"abc", "def", "ghi.mit.edu"})
	assert.NoError(t, err)

	err = server.StartWriteReplicated(0, 0, []byte("|||"), []apis.ServerAddress{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hello world 01")
}

func TestChunkserver_Replicate(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("Replicate", apis.ChunkNum(74), apis.ServerAddress("jkl.mit.edu"), apis.Version(56)).Return(nil)
	mocked.On("Replicate", apis.ChunkNum(0), apis.ServerAddress(""), apis.Version(0)).Return(errors.New("hello world 02"))

	assert.NoError(t, server.Replicate(74, "jkl.mit.edu", 56))

	err := server.Replicate(0, "", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hello world 02")
}

func TestChunkserver_Read(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("Read", apis.ChunkNum(75), uint32(57), uint32(58), apis.Version(59)).Return([]byte("testy testy"), apis.Version(60), nil)
	mocked.On("Read", apis.ChunkNum(0), uint32(0), uint32(0), apis.Version(0)).Return(nil, apis.Version(6), errors.New("hello world 03"))

	data, ver, err := server.Read(75, 57, 58, 59)
	assert.NoError(t, err)
	assert.Equal(t, "testy testy", string(data))
	assert.Equal(t, apis.Version(60), ver)

	_, ver, err = server.Read(0, 0, 0, 0)
	assert.Error(t, err)
	assert.Equal(t, apis.Version(6), ver)
	assert.Contains(t, err.Error(), "hello world 03")
}

func TestChunkserver_StartWrite(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("StartWrite", apis.ChunkNum(76), uint32(61), []byte("phenomenologist")).Return(nil)
	mocked.On("StartWrite", apis.ChunkNum(0), uint32(0), []byte(nil)).Return(errors.New("hello world 04"))

	assert.NoError(t, server.StartWrite(76, 61, []byte("phenomenologist")))

	err := server.StartWrite(0, 0, []byte{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hello world 04")
}

func TestChunkserver_CommitWrite(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("CommitWrite", apis.ChunkNum(77), apis.CommitHash("this is my hash"), apis.Version(62), apis.Version(63)).Return(nil)
	mocked.On("CommitWrite", apis.ChunkNum(0), apis.CommitHash(""), apis.Version(0), apis.Version(0)).Return(errors.New("hello world 05"))

	assert.NoError(t, server.CommitWrite(77, "this is my hash", 62, 63))

	err := server.CommitWrite(0, "", 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hello world 05")
}

func TestChunkserver_UpdateLatestVersion(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("UpdateLatestVersion", apis.ChunkNum(78), apis.Version(64), apis.Version(65)).Return(nil)
	mocked.On("UpdateLatestVersion", apis.ChunkNum(0), apis.Version(0), apis.Version(0)).Return(errors.New("hello world 06"))

	assert.NoError(t, server.UpdateLatestVersion(78, 64, 65))

	err := server.UpdateLatestVersion(0, 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hello world 06")
}

func TestChunkserver_Add(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("Add", apis.ChunkNum(79), []byte("quest"), apis.Version(66)).Return(nil)
	mocked.On("Add", apis.ChunkNum(0), []byte(nil), apis.Version(0)).Return(errors.New("hello world 07"))

	assert.NoError(t, server.Add(79, []byte("quest"), 66))

	err := server.Add(0, []byte{}, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hello world 07")
}

func TestChunkserver_Delete(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("Delete", apis.ChunkNum(80), apis.Version(67)).Return(nil)
	mocked.On("Delete", apis.ChunkNum(0), apis.Version(0)).Return(errors.New("hello world 08"))

	assert.NoError(t, server.Delete(80, 67))

	err := server.Delete(0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hello world 08")
}

func TestChunkserver_ListAllChunks_Pass(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("ListAllChunks").Return([]apis.ChunkVersion{
		{81, 68}, {82, 69},
	}, nil)

	chunks, err := server.ListAllChunks()
	assert.NoError(t, err)
	assert.Equal(t, []apis.ChunkVersion{
		{81, 68}, {82, 69},
	}, chunks)
}

func TestChunkserver_ListAllChunks_Fail(t *testing.T) {
	mocked, teardown, server := beginChunkserverTest(t)
	defer teardown()

	mocked.On("ListAllChunks").Return([]apis.ChunkVersion{},
		errors.New("hello world 09"))

	chunks, err := server.ListAllChunks()
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "hello world 09")
	}
	assert.Empty(t, chunks)
}
