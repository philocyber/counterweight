package traffic

// This shim delegates to the pinned modernc SQLite native VFS.
// Its ABI adapters follow modernc.org/sqlite/lib's generated callback convention.

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
	"modernc.org/libc/sys/types"
	sqlite3 "modernc.org/sqlite/lib"
)

var walVFSSequence atomic.Uint64
var walVFSOwners sync.Map // native VFS pointer -> *boundedWALVFS
var walVFSFiles sync.Map  // native file pointer -> *boundedWALFile

type boundedWALVFS struct {
	name               string
	limit              int64
	journalLimit       int64
	tempLimit          int64
	scratchLimit       int64
	scratchUsed        int64 // protected by mu; live temporary files, not the main journal
	parent, ptr, cname uintptr
	mu                 sync.Mutex
	files              int
	connections        int
	closing            bool
	closed             bool
	growthCheck        func(int64) error // immutable; called under mu before native growth
}

type boundedWALFile struct {
	owner           *boundedWALVFS
	original        sqlite3.Tsqlite3_io_methods
	originalPointer uintptr
	methods         uintptr
	wal             bool
	limit           int64
	scratch         bool
	allocated       int64 // protected by owner.mu
}

func sqliteCallback(fn any) uintptr {
	// Same representation used by the pinned library's __ccgo_fp helper.
	return (*[2]uintptr)(unsafe.Pointer(&fn))[1]
}

func newBoundedWALVFS(limit int64) (*boundedWALVFS, error) {
	if limit < 65536 {
		return nil, fmt.Errorf("WAL write limit must be at least 65536 bytes")
	}
	tls := libc.NewTLS()
	defer tls.Close()
	parent := sqlite3.Xsqlite3_vfs_find(tls, 0)
	if parent == 0 {
		return nil, fmt.Errorf("native SQLite VFS unavailable")
	}
	v := &boundedWALVFS{name: fmt.Sprintf("counterweight-wal-%d", walVFSSequence.Add(1)), limit: limit, parent: parent}
	var err error
	v.cname, err = libc.CString(v.name)
	if err != nil {
		return nil, err
	}
	v.ptr = libc.Xmalloc(tls, types.Size_t(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
	if v.ptr == 0 {
		libc.Xfree(tls, v.cname)
		return nil, fmt.Errorf("allocating SQLite VFS")
	}
	native := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(v.ptr))
	*native = *(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(parent))
	native.FpNext, native.FzName, native.FxOpen = 0, v.cname, sqliteCallback(boundedWALOpen)
	walVFSOwners.Store(v.ptr, v)
	if code := sqlite3.Xsqlite3_vfs_register(tls, v.ptr, 0); code != sqlite3.SQLITE_OK {
		walVFSOwners.Delete(v.ptr)
		libc.Xfree(tls, v.ptr)
		libc.Xfree(tls, v.cname)
		return nil, fmt.Errorf("registering bounded WAL VFS: %d", code)
	}
	return v, nil
}

// Close requires all SQLite connections/handles to be closed first. It refuses
// to free live callbacks; callers may close remaining handles and retry.
func (v *boundedWALVFS) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.closeLocked()
}

func (v *boundedWALVFS) closeLocked() error {
	if v.closed {
		return nil
	}
	if v.files != 0 || v.connections != 0 {
		return fmt.Errorf("bounded WAL VFS still has %d open files", v.files)
	}
	tls := libc.NewTLS()
	defer tls.Close()
	if code := sqlite3.Xsqlite3_vfs_unregister(tls, v.ptr); code != sqlite3.SQLITE_OK {
		return fmt.Errorf("unregistering bounded WAL VFS: %d", code)
	}
	v.closed = true
	walVFSOwners.Delete(v.ptr)
	libc.Xfree(tls, v.ptr)
	libc.Xfree(tls, v.cname)
	return nil
}

func (v *boundedWALVFS) retainConnection() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closing || v.closed {
		return fmt.Errorf("traffic WAL VFS is closing")
	}
	v.connections++
	return nil
}

func (v *boundedWALVFS) releaseConnection() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.connections--
	if v.closing && v.connections == 0 {
		return v.closeLocked()
	}
	return nil
}

// sql.DB.Close can return before checked-out connections close. Keep this
// registration alive until their native Close methods have finished.
func (v *boundedWALVFS) requestClose() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closing = true
	if v.connections == 0 {
		return v.closeLocked()
	}
	return nil
}

func boundedWALOpen(tls *libc.TLS, vfs, name, file uintptr, flags int32, outFlags uintptr) int32 {
	value, found := walVFSOwners.Load(vfs)
	if !found {
		return sqlite3.SQLITE_CANTOPEN
	}
	v := value.(*boundedWALVFS)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return sqlite3.SQLITE_CANTOPEN
	}
	open := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(v.parent)).FxOpen
	code := (*(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{open})))(tls, v.parent, name, file, flags, outFlags)
	if code != sqlite3.SQLITE_OK {
		return code
	}
	native := (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file))
	if native.FpMethods == 0 {
		return sqlite3.SQLITE_CANTOPEN
	}
	state := &boundedWALFile{owner: v, original: *(*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(native.FpMethods)), originalPointer: native.FpMethods, wal: flags&sqlite3.SQLITE_OPEN_WAL != 0}
	if state.wal {
		state.limit = v.limit
	} else if flags&(sqlite3.SQLITE_OPEN_MAIN_JOURNAL|sqlite3.SQLITE_OPEN_TEMP_JOURNAL|sqlite3.SQLITE_OPEN_SUBJOURNAL|sqlite3.SQLITE_OPEN_SUPER_JOURNAL) != 0 {
		state.limit = v.journalLimit
	} else if flags&(sqlite3.SQLITE_OPEN_TEMP_DB|sqlite3.SQLITE_OPEN_TRANSIENT_DB) != 0 {
		state.limit = v.tempLimit
	}
	state.scratch = v.scratchLimit > 0 && flags&(sqlite3.SQLITE_OPEN_TEMP_DB|sqlite3.SQLITE_OPEN_TRANSIENT_DB|sqlite3.SQLITE_OPEN_TEMP_JOURNAL|sqlite3.SQLITE_OPEN_SUBJOURNAL) != 0
	if state.scratch {
		size, sizeCode := state.nativeSize(tls, file)
		if sizeCode != sqlite3.SQLITE_OK || size < 0 || size > v.scratchLimit-v.scratchUsed {
			close := state.original.FxClose
			(*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{close})))(tls, file)
			native.FpMethods = 0
			if sizeCode != sqlite3.SQLITE_OK {
				return sizeCode
			}
			return sqlite3.SQLITE_FULL
		}
		state.allocated = size
	}
	state.methods = libc.Xmalloc(tls, types.Size_t(unsafe.Sizeof(sqlite3.Tsqlite3_io_methods{})))
	if state.methods == 0 {
		close := state.original.FxClose
		(*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{close})))(tls, file)
		native.FpMethods = 0
		return sqlite3.SQLITE_NOMEM
	}
	methods := (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(state.methods))
	*methods = state.original
	methods.FxClose = sqliteCallback(boundedWALClose)
	methods.FxWrite = sqliteCallback(boundedWALWrite)
	methods.FxTruncate = sqliteCallback(boundedWALTruncate)
	methods.FxFileControl = sqliteCallback(boundedWALControl)
	walVFSFiles.Store(file, state)
	if state.scratch {
		v.scratchUsed += state.allocated
	}
	v.files++
	native.FpMethods = state.methods
	return sqlite3.SQLITE_OK
}

func boundedWALClose(tls *libc.TLS, file uintptr) int32 {
	value, found := walVFSFiles.LoadAndDelete(file)
	if !found {
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	state := value.(*boundedWALFile)
	// Restore the delegate table before native cleanup inspects its file.
	(*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods = state.originalPointer
	close := state.original.FxClose
	code := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{close})))(tls, file)
	libc.Xfree(tls, state.methods)
	state.owner.mu.Lock()
	if state.scratch && code == sqlite3.SQLITE_OK {
		state.owner.scratchUsed -= state.allocated
	}
	state.owner.files--
	state.owner.mu.Unlock()
	return code
}

func boundedWALWrite(tls *libc.TLS, file, buffer uintptr, amount int32, offset sqlite3.Tsqlite3_int64) int32 {
	value, found := walVFSFiles.Load(file)
	if !found {
		return sqlite3.SQLITE_IOERR_WRITE
	}
	state := value.(*boundedWALFile)
	if amount < 0 || offset < 0 {
		return sqlite3.SQLITE_IOERR_WRITE
	}
	if int64(offset) > int64(1<<63-1)-int64(amount) || (state.limit > 0 && (int64(amount) > state.limit || int64(offset) > state.limit-int64(amount))) {
		return sqlite3.SQLITE_FULL
	}
	write := state.original.FxWrite
	state.owner.mu.Lock()
	defer state.owner.mu.Unlock()
	if code := state.checkGrowth(tls, file, int64(offset)+int64(amount)); code != sqlite3.SQLITE_OK {
		return code
	}
	if state.scratch {
		if !state.reserveScratch(int64(offset) + int64(amount)) {
			return sqlite3.SQLITE_FULL
		}
	}
	return (*(*func(*libc.TLS, uintptr, uintptr, int32, sqlite3.Tsqlite3_int64) int32)(unsafe.Pointer(&struct{ uintptr }{write})))(tls, file, buffer, amount, offset)
}

func boundedWALTruncate(tls *libc.TLS, file uintptr, size sqlite3.Tsqlite3_int64) int32 {
	value, found := walVFSFiles.Load(file)
	if !found {
		return sqlite3.SQLITE_IOERR_TRUNCATE
	}
	state := value.(*boundedWALFile)
	if size < 0 {
		return sqlite3.SQLITE_IOERR_TRUNCATE
	}
	if state.limit > 0 && int64(size) > state.limit {
		return sqlite3.SQLITE_FULL
	}
	truncate := state.original.FxTruncate
	state.owner.mu.Lock()
	defer state.owner.mu.Unlock()
	if code := state.checkGrowth(tls, file, int64(size)); code != sqlite3.SQLITE_OK {
		return code
	}
	if state.scratch {
		if !state.reserveScratch(int64(size)) {
			return sqlite3.SQLITE_FULL
		}
	}
	code := (*(*func(*libc.TLS, uintptr, sqlite3.Tsqlite3_int64) int32)(unsafe.Pointer(&struct{ uintptr }{truncate})))(tls, file, size)
	if state.scratch && code == sqlite3.SQLITE_OK {
		state.setScratchSize(int64(size))
	}
	return code
}

func boundedWALControl(tls *libc.TLS, file uintptr, operation int32, argument uintptr) int32 {
	value, found := walVFSFiles.Load(file)
	if !found {
		return sqlite3.SQLITE_IOERR
	}
	state := value.(*boundedWALFile)
	if operation == sqlite3.SQLITE_FCNTL_SIZE_HINT {
		state.owner.mu.Lock()
		defer state.owner.mu.Unlock()
	}
	if operation == sqlite3.SQLITE_FCNTL_SIZE_HINT {
		if argument == 0 {
			return sqlite3.SQLITE_IOERR
		}
		if size := *(*sqlite3.Tsqlite3_int64)(unsafe.Pointer(argument)); size < 0 || (state.limit > 0 && int64(size) > state.limit) {
			return sqlite3.SQLITE_FULL
		}
		if code := state.checkGrowth(tls, file, int64(*(*sqlite3.Tsqlite3_int64)(unsafe.Pointer(argument)))); code != sqlite3.SQLITE_OK {
			return code
		}
		if state.scratch && !state.reserveScratch(int64(*(*sqlite3.Tsqlite3_int64)(unsafe.Pointer(argument)))) {
			return sqlite3.SQLITE_FULL
		}
	}
	if operation == sqlite3.SQLITE_FCNTL_CHUNK_SIZE {
		if argument == 0 {
			return sqlite3.SQLITE_IOERR
		}
		// Native chunk allocation may round a later valid write above the cap.
		if *(*int32)(unsafe.Pointer(argument)) != 0 {
			return sqlite3.SQLITE_FULL
		}
	}
	control := state.original.FxFileControl
	code := (*(*func(*libc.TLS, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{control})))(tls, file, operation, argument)
	if state.scratch && operation == sqlite3.SQLITE_FCNTL_SIZE_HINT {
		if size, sizeCode := state.nativeSize(tls, file); sizeCode == sqlite3.SQLITE_OK && size >= 0 && size <= state.allocated {
			state.setScratchSize(size)
		}
	}
	return code
}

func (state *boundedWALFile) nativeSize(tls *libc.TLS, file uintptr) (int64, int32) {
	// Native callbacks receive uintptr, so keep the output in the pinned
	// runtime's stable allocation rather than a movable Go stack local.
	size := tls.Alloc(8)
	defer tls.Free(8)
	*(*sqlite3.Tsqlite3_int64)(unsafe.Pointer(size)) = 0
	callback := state.original.FxFileSize
	code := (*(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{callback})))(tls, file, size)
	return int64(*(*sqlite3.Tsqlite3_int64)(unsafe.Pointer(size))), code
}

// Callers serialize this check and the delegate allocation with owner.mu.
func (state *boundedWALFile) checkGrowth(tls *libc.TLS, file uintptr, end int64) int32 {
	if state.owner.growthCheck == nil {
		return sqlite3.SQLITE_OK
	}
	size, code := state.nativeSize(tls, file)
	if code != sqlite3.SQLITE_OK {
		return code
	}
	if size < 0 {
		return sqlite3.SQLITE_IOERR
	}
	if end > size && state.owner.growthCheck(end-size) != nil {
		return sqlite3.SQLITE_FULL
	}
	return sqlite3.SQLITE_OK
}

// Callers hold owner.mu across reservation and native allocation. A failed
// native write may be partial, so its reservation remains until truncation,
// a confirmed size hint result, or successful close releases it.
func (state *boundedWALFile) reserveScratch(size int64) bool {
	if size <= state.allocated {
		return true
	}
	delta := size - state.allocated
	if delta > state.owner.scratchLimit-state.owner.scratchUsed {
		return false
	}
	state.owner.scratchUsed += delta
	state.allocated = size
	return true
}

func (state *boundedWALFile) setScratchSize(size int64) {
	state.owner.scratchUsed += size - state.allocated
	state.allocated = size
}
