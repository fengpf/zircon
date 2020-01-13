package filesystem

import (
	"io"
	"os"
	path2 "path"
	"time"
	"errors"
	"zircon/lib/apis"
	"zircon/lib/client"
	"zircon/lib/rpc"
	"zircon/lib/filesystem/syncserver"
	"fmt"
)

type filesystem struct {
	t *Traverser
}

type Configuration struct {
	MountPoint          string
	ClientConfig        client.Configuration
	SyncServerAddresses []apis.ServerAddress
}

func NewFilesystemClient(config Configuration) (Filesystem, error) {
	if len(config.SyncServerAddresses) == 0 {
		return nil, errors.New("no syncservers specified")
	}
	cli, err := client.ConfigureNetworkedClient(config.ClientConfig)
	if err != nil {
		return nil, err
	}
	sscache := rpc.NewConnectionCache()
	var ss []apis.SyncServer
	for _, ssaddr := range config.SyncServerAddresses {
		server, err := sscache.SubscribeSyncServer(ssaddr)
		if err != nil {
			return nil, err
		}
		ss = append(ss, server)
	}
	return NewFilesystem(cli, syncserver.RoundRobin(ss)), nil
}

func NewFilesystem(client apis.Client, sync apis.SyncServer) Filesystem {
	return &filesystem{
		t: &Traverser{
			client: client,
			fs: FilesystemSync{
				s: sync,
			},
		},
	}
}

func (f *filesystem) Mkdir(path string) error {
	ref, err := f.t.PathDir(path2.Dir(path))
	if err != nil {
		return err
	}
	defer ref.Release()
	return ref.NewDir(path2.Base(path))
}

func (f *filesystem) Rename(source string, dest string) error {
	srcDir, err := f.t.PathDir(path2.Dir(source))
	if err != nil {
		return err
	}
	defer srcDir.Release()
	destDir, err := f.t.PathDir(path2.Dir(dest))
	if err != nil {
		return err
	}
	defer destDir.Release()
	return srcDir.MoveTo(destDir, source, dest)
}

func (f *filesystem) Unlink(path string) error {
	ref, err := f.t.PathDir(path2.Dir(path))
	if err != nil {
		return err
	}
	defer ref.Release()
	return ref.Remove(path2.Base(path), false)
}

func (f *filesystem) Rmdir(path string) error {
	ref, err := f.t.PathDir(path2.Dir(path))
	if err != nil {
		return err
	}
	defer ref.Release()
	return ref.Remove(path2.Base(path), true)
}

func (f *filesystem) SymLink(source string, dest string) error {
	ref, err := f.t.PathDir(path2.Dir(source))
	if err != nil {
		return err
	}
	defer ref.Release()
	return ref.NewSymLink(path2.Base(source), dest)
}

type fsFileInfo struct {
	name string
	size int64
	isdir bool
}

func (f fsFileInfo) Name() string {
	return f.name
}

func (f fsFileInfo) Size() int64 {
	return f.size
}

func (f fsFileInfo) Mode() os.FileMode {
	if f.isdir {
		return os.FileMode(0040755)
	} else {
		return os.FileMode(0100755)
	}
}

func (f fsFileInfo) ModTime() time.Time {
	return time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
}

func (f fsFileInfo) IsDir() bool {
	return f.isdir
}

func (f fsFileInfo) Sys() interface{} {
	return nil
}

func (f *filesystem) Stat(path string) (os.FileInfo, error) {
	ref, err := f.t.PathDir(path2.Dir(path))
	if err != nil {
		return nil, err
	}
	defer ref.Release()
	var ntype NodeType
	if path == "/" {
		ntype = DIRECTORY
	} else {
		ntype, err = ref.Stat(path2.Base(path))
		if err != nil {
			return nil, err
		}
	}
	switch ntype {
	case NONEXISTENT:
		return nil, errors.New("no such file")
	case FILE:
		f, err := ref.LookupFile(path2.Base(path))
		if err != nil {
			return nil, err
		}
		defer f.Release()
		size, err := f.Size()
		if err != nil {
			return nil, err
		}
		return fsFileInfo{
			name: path2.Base(path),
			isdir: false,
			size: int64(size),
		}, nil
	case DIRECTORY:
		var r *Reference
		if path == "/" {
			r, err = f.t.Root()
		} else {
			r, err = ref.LookupDir(path2.Base(path))
		}
		if err != nil {
			return nil, err
		}
		defer r.Release()
		entries, _, err := r.listEntries()
		if err != nil {
			return nil, err
		}
		return fsFileInfo{
			name: path2.Base(path),
			isdir: true,
			size: int64(EntrySize * len(entries)),
		}, nil
	case SYMLINK:
		link, err := ref.LookupSymLink(path2.Base(path))
		if err != nil {
			return nil, err
		}
		return fsFileInfo{
			name: path2.Base(path),
			isdir: false,
			size: int64(len(link)),
		}, nil
	default:
		return nil, errors.New("internal error: invalid stat result")
	}
}

func (f *filesystem) ReadLink(path string) (string, error) {
	ref, err := f.t.PathDir(path2.Dir(path))
	if err != nil {
		return "", err
	}
	defer ref.Release()
	link, err := ref.LookupSymLink(path2.Base(path))
	if err != nil {
		return "", err
	}
	return link, nil
}

func (f *filesystem) ListDir(path string) ([]string, error) {
	ref, err := f.t.PathDir(path)
	if err != nil {
		return nil, err
	}
	defer ref.Release()
	entries, _, err := ref.listEntries()
	if err != nil {
		return nil, err
	}
	elements := make([]string, len(entries))
	for i, entry := range entries {
		elements[i] = entry.Name
	}
	return elements, nil
}

func (f *filesystem) Truncate(path string, length uint32) error {
	ref, err := f.t.PathDir(path2.Dir(path))
	if err != nil {
		return err
	}
	defer ref.Release()
	file, err := ref.LookupFile(path2.Base(path))
	if err != nil {
		return err
	}
	defer file.Release()
	return file.Truncate(length)
}

func (f *filesystem) OpenRead(path string) (ReadOnlyFile, error) {
	ref, err := f.t.PathDir(path2.Dir(path))
	if err != nil {
		return nil, err
	}
	defer ref.Release()
	file, err := ref.LookupFile(path2.Base(path))
	if err != nil {
		return nil, err
	}
	return &fileStream{
		f: file,
	}, nil
}

// NOTE: closing file results is INCREDIBLY IMPORTANT
func (f *filesystem) OpenWrite(path string, create bool, exclusive bool) (WritableFile, error) {
	ref, err := f.t.PathDir(path2.Dir(path))
	if err != nil {
		return nil, err
	}
	defer ref.Release()
	var file *File
	if exclusive {
		if !create {
			return nil, errors.New("mismatched exclusive/create options")
		}
		err := ref.NewFile(path2.Base(path))
		if err != nil {
			return nil, err
		}
		file, err = ref.LookupFile(path2.Base(path))
		if err != nil {
			return nil, err
		}
	} else {
		file, err = ref.LookupFile(path2.Base(path))
		if err != nil {
			if create {
				err2 := ref.NewFile(path2.Base(path))
				if err2 != nil {
					return nil, fmt.Errorf("two errors: %v -- and -- %v", err, err2)
				}
				file, err2 = ref.LookupFile(path2.Base(path))
				if err2 != nil {
					return nil, fmt.Errorf("two errors: %v -- and -- %v", err, err2)
				}
			} else {
				return nil, err
			}
		}
	}
	return &fileStream{
		f: file,
	}, nil
}

func (f *filesystem) GetTraverser() (*Traverser, error) {
	return f.t, nil
}

type ReadOnlyFile interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

type WritableFile interface {
	io.Reader
	io.ReaderAt
	io.Writer
	io.WriterAt
	io.Seeker
	io.Closer
	Truncate(uint64) error
}

type erroringWriter struct {
	base ReadOnlyFile
}

func WithErroringWrite(i ReadOnlyFile) WritableFile {
	return erroringWriter{i}
}

func (f erroringWriter) Read(p []byte) (n int, err error) {
	return f.base.Read(p)
}

func (f erroringWriter) ReadAt(p []byte, off int64) (n int, err error) {
	return f.base.ReadAt(p, off)
}

func (f erroringWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("not a writable file")
}

func (f erroringWriter) WriteAt(p []byte, off int64) (n int, err error) {
	return 0, errors.New("not a writable file")
}

func (f erroringWriter) Truncate(len uint64) error {
	return errors.New("not a writable file")
}

func (f erroringWriter) Seek(offset int64, whence int) (int64, error) {
	return f.base.Seek(offset, whence)
}

func (f erroringWriter) Close() error {
	return f.base.Close()
}

type fileStream struct {
	f      *File
	closed bool
	head   uint32
}

var _ WritableFile = &fileStream{}

func (f *fileStream) Read(p []byte) (n int, err error) {
	if f.closed {
		return 0, errors.New("file already closed")
	}
	data, err := f.f.Read(f.head, uint32(len(p)))
	if err != nil {
		return 0, err
	}
	if len(data) == 0 && len(p) > 0 {
		return 0, io.EOF
	}
	copy(p, data)
	f.head += uint32(len(data))
	return len(data), nil
}

func (f *fileStream) ReadAt(p []byte, off int64) (n int, err error) {
	if f.closed {
		return 0, errors.New("file already closed")
	}
	// TODO: overflow checks
	data, err := f.f.Read(uint32(off), uint32(len(p)))
	if err != nil {
		return 0, err
	}
	copy(p, data)
	if len(data) < len(p) {
		return len(data), io.EOF
	} else {
		return len(data), nil
	}
}

func (f *fileStream) Write(p []byte) (n int, err error) {
	if f.closed {
		return 0, errors.New("file already closed")
	}
	err = f.f.Write(f.head, p)
	if err != nil {
		return 0, err
	}
	f.head += uint32(len(p))
	return len(p), nil
}

func (f *fileStream) WriteAt(p []byte, off int64) (n int, err error) {
	if f.closed {
		return 0, errors.New("file already closed")
	}
	// TODO: overflow checks
	err = f.f.Write(uint32(off), p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (f *fileStream) Seek(offset int64, whence int) (int64, error) {
	if f.closed {
		return 0, errors.New("file already closed")
	}
	var nhead uint32
	// TODO: handle overflow
	if whence == io.SeekStart {
		nhead = uint32(offset)
	} else if whence == io.SeekCurrent {
		nhead = uint32(int64(f.head) + offset)
	} else if whence == io.SeekEnd {
		size, err := f.f.Size()
		if err != nil {
			return 0, err
		}
		nhead = uint32(int64(size) + offset)
	}
	f.head = nhead
	return int64(nhead), nil
}

func (f *fileStream) Truncate(len uint64) error {
	// TODO: handle overflow
	return f.f.Truncate(uint32(len))
}

func (f *fileStream) Close() error {
	if !f.closed {
		f.f.Release()
		f.closed = true
	}
	return nil
}
