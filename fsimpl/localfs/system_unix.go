//go:build !windows

// Why not os.Root on Linux (and Unix):
//
//  1. os.Root follows symbolic links whose targets stay inside the root; it has
//     no O_NOFOLLOW, AT_SYMLINK_NOFOLLOW, or RESOLVE_NO_SYMLINKS mode. 9P2000.L
//     servers must never follow symlinks (clients resolve them via Treadlink
//     in their own mount namespace), especially when wrapping RootAttacher()
//     with a sub-namespace filter such as u-root/cpu.
//  2. Go's os.Root documentation explicitly notes symlink TOCTOU races on
//     Root.Chmod, Root.Chown, and Root.Chtimes on Unix (and it has no Lchmod
//     or Lutimes).
//  3. os.Root has no xattr API, no Truncate, no AT_REMOVEDIR, and does not
//     expose its underlying directory fd.
//  4. Linux O_PATH + openat2(RESOLVE_NO_SYMLINKS|RESOLVE_BENEATH) +
//     /proc/self/fd traverses 0111 execute-only directories in one syscall and
//     operates atomically on symlink or mode-0000 inodes without opening FIFOs
//     or device nodes.

package localfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

// linuxATRemoveDir is the 9P2000.L wire flag for Tunlinkat (AT_REMOVEDIR).
const linuxATRemoveDir = 0x200

func umask(mask int) int {
	return syscall.Umask(mask)
}

type rootState struct {
	// mu is a leaf lock (never acquired while holding another lock, and no
	// other lock is ever acquired under mu). It protects fd and refs.
	//
	// Invariant: syscalls (openRootFD, openAt, fstat) and runtime.GC()
	// retries never run with mu held. acquireFD drops mu before reopening
	// so runtime.AddCleanup calling (*rootState).close can never deadlock
	// against an EMFILE GC retry or slow I/O.
	mu   sync.Mutex
	fd   int
	refs int
}

func (s *rootState) closeLocked() {
	if s.fd >= 0 {
		_ = unix.Close(s.fd)
		s.fd = -1
	}
}

func (s *rootState) close() {
	s.mu.Lock()
	s.closeLocked()
	s.mu.Unlock()
}

func (s *rootState) incRef() {
	s.mu.Lock()
	s.refs++
	s.mu.Unlock()
}

func (s *rootState) decRef() {
	s.mu.Lock()
	s.refs--
	if s.refs == 0 {
		s.closeLocked()
	}
	s.mu.Unlock()
}

func openRootFD(path string) (int, devino, error) {
	const rootFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOCTTY | unix.O_CLOEXEC
	fd, err := openAt(unix.AT_FDCWD, path, rootFlags, 0)
	if err == unix.EMFILE || err == unix.ENFILE {
		// Reclaim abandoned cleanup-owned root fds and retry once.
		runtime.GC()
		runtime.Gosched()
		fd, err = openAt(unix.AT_FDCWD, path, rootFlags, 0)
	}
	if err != nil {
		return -1, devino{}, err
	}

	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, devino{}, err
	}
	return fd, devino{dev: uint64(st.Dev), ino: st.Ino}, nil
}

// rootDir pins the export root directory fd shared by all Local instances from
// a single Attach. Reference counts (active fids + in-flight ops) close the fd
// immediately when the last fid clunks so host mountpoints can be unmounted
// without EBUSY. If an operation runs after refs drops to 0 (e.g. p9's
// Txattrwalk fid aliasing), acquireFD lazily reopens path and verifies its
// (dev, ino) identity (ESTALE on mismatch). runtime.AddCleanup reclaims the fd
// if callers abandon a root without calling Close.
type rootDir struct {
	path  string
	id    devino
	state *rootState
}

func openRootDir(path string) (*rootDir, error) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	fd, id, err := openRootFD(path)
	if err != nil {
		return nil, err
	}

	state := &rootState{fd: fd, refs: 1}
	r := &rootDir{
		path:  path,
		id:    id,
		state: state,
	}
	runtime.AddCleanup(r, (*rootState).close, state)
	return r, nil
}

func (r *rootDir) acquireFID() { r.state.incRef() }
func (r *rootDir) releaseFID() { r.state.decRef() }

func (r *rootDir) acquireFD() (int, func(), error) {
	s := r.state
	s.mu.Lock()
	if s.fd < 0 {
		s.mu.Unlock()

		fd, id, err := openRootFD(r.path)
		if err != nil {
			return -1, nil, err
		}
		if id != r.id {
			_ = unix.Close(fd)
			return -1, nil, unix.ESTALE
		}

		s.mu.Lock()
		if s.fd < 0 {
			s.fd = fd
		} else {
			_ = unix.Close(fd)
		}
	}
	fd := s.fd
	s.refs++
	s.mu.Unlock()
	return fd, func() {
		s.decRef()
		runtime.KeepAlive(r)
	}, nil
}

// openDir returns a directory file descriptor for rel inside r, traversed
// without following any symbolic links. The caller must invoke release.
func (r *rootDir) openDir(rel string) (int, func(), error) {
	if r == nil {
		return -1, nil, linux.EBADF
	}
	if err := validateRel(rel); err != nil {
		return -1, nil, err
	}
	rootFD, releaseRoot, err := r.acquireFD()
	if err != nil {
		return -1, nil, err
	}
	if rel == "" {
		return rootFD, releaseRoot, nil
	}
	dirFD, err := openDirRel(rootFD, rel)
	releaseRoot()
	if err != nil {
		return -1, nil, err
	}
	return dirFD, func() { _ = unix.Close(dirFD) }, nil
}

func ignoringEINTR(fn func() error) error {
	for {
		err := fn()
		if err != unix.EINTR {
			return err
		}
	}
}

func ignoringEINTRInt(fn func() (int, error)) (int, error) {
	for {
		n, err := fn()
		if err != unix.EINTR {
			return n, err
		}
	}
}

func openAt(dirFD int, name string, flags int, mode uint32) (int, error) {
	return ignoringEINTRInt(func() (int, error) {
		return unix.Openat(dirFD, name, flags, mode)
	})
}

func fstat(fd int, st *unix.Stat_t) error {
	return ignoringEINTR(func() error {
		return unix.Fstat(fd, st)
	})
}

func fstatAt(dirFD int, name string, st *unix.Stat_t, flags int) error {
	return ignoringEINTR(func() error {
		return unix.Fstatat(dirFD, name, st, flags)
	})
}

func withFileFD(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var fnErr error
	if err := rc.Control(func(fd uintptr) {
		fnErr = fn(int(fd))
	}); err != nil {
		return err
	}
	return fnErr
}

const unixAllowedOpenFlags = unix.O_TRUNC | unix.O_APPEND | unix.O_NONBLOCK | unix.O_SYNC

func sameInode(a, b *unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode&unix.S_IFMT == b.Mode&unix.S_IFMT
}

// openVerifiedFD opens (dirFD, name) without following symlinks and verifies
// the opened descriptor matches want's (dev, ino), returning ESTALE otherwise.
func openVerifiedFD(dirFD int, name string, access int, want *unix.Stat_t) (int, error) {
	fd, err := openAt(dirFD, name, access|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	var got unix.Stat_t
	if err := fstat(fd, &got); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if !sameInode(&got, want) {
		_ = unix.Close(fd)
		return -1, unix.ESTALE
	}
	return fd, nil
}

func verifyTargetMatchesFile(f *os.File, dirFD int, name string) error {
	var openSt unix.Stat_t
	if err := withFileFD(f, func(fd int) error {
		return fstat(fd, &openSt)
	}); err != nil {
		return err
	}
	var targetSt unix.Stat_t
	if err := fstatAt(dirFD, name, &targetSt, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if !sameInode(&openSt, &targetSt) {
		return unix.ESTALE
	}
	return nil
}

func checkRegularForTruncate(st *unix.Stat_t) error {
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		return nil
	case unix.S_IFLNK:
		return unix.ELOOP
	case unix.S_IFDIR:
		return unix.EISDIR
	default:
		return unix.EINVAL
	}
}

// openDirStepByStep traverses a non-empty validated relative path component by
// component from rootFD, refusing any symlink or non-directory component.
func openDirStepByStep(rootFD int, rel string) (int, error) {
	cur := rootFD
	for comp := range strings.SplitSeq(rel, "/") {
		next, err := openAt(cur, comp, openDirFlags|unix.O_NOFOLLOW, 0)
		if cur != rootFD {
			_ = unix.Close(cur)
		}
		if err != nil {
			return -1, err
		}
		cur = next
	}
	return cur, nil
}

func (l *Local) openDir() (int, func(), error) {
	return l.root.openDir(l.rel)
}

// openParent returns a directory fd for the parent of l.rel together with the
// leaf component name. It fails with EINVAL if l refers to the export root.
func (l *Local) openParent() (int, func(), string, error) {
	if l.root == nil {
		return -1, nil, "", linux.EBADF
	}
	if l.rel == "" {
		return -1, nil, "", linux.EINVAL
	}
	dir, base := splitRel(l.rel)
	if err := validateName(base); err != nil {
		return -1, nil, "", err
	}
	dirFD, release, err := l.root.openDir(dir)
	if err != nil {
		return -1, nil, "", err
	}
	return dirFD, release, base, nil
}

// openTarget returns (dirFD, release, name) referencing l itself: (rootFD,
// release, ".") for the export root or (parentFD, release, base) otherwise.
func (l *Local) openTarget() (int, func(), string, error) {
	if l.rel == "" {
		dirFD, release, err := l.root.openDir("")
		if err != nil {
			return -1, nil, "", err
		}
		return dirFD, release, ".", nil
	}
	return l.openParent()
}

func (l *Local) displayName() string {
	if l.rel == "" {
		return l.root.path
	}
	return l.rel
}

// Inode numbers are only unique within a given device.  We need to compress
// device (u64) and inode number (u64) to a 64 bit qid path.
//
// We could assign a path for each new file we see and save the {dev,ino} -> qid
// lookup in a map.  Big downside: wasted memory.
//
// To avoid making a map entry for every file we ever see, realize that a lot of
// bits are 0 in the common case.  Let's pick bits that are likely set, and make
// a deterministic function to compute the qid.path for them.  For the cases
// that have any unlikely bits, stuff them in the map.  And differentiate
// between these two scenarios with the leading bit of qid.path.
//
// dev_t:
// 63                             32                              0
// |------------------------------|-------------------------------|
// \-------nothing really---------|\----MAJOR---AND----MINOR------|
//
// - The top 32 bits are usually 0.  The kernel's dev_t is 32 bits.
// - Major and minor aren't nicely laid out.  We'll just extract them from
// dev_t using unix helpers.
// - The MAJOR number space is used a lot.  I regularly see at least 9 of 12 bits
// used.  Let's not chop off any.
// - For MINOR, let's chop off some upper bits.
//
// If we want to be clever, we could look at all the devices currently in the
// system and dynamically generate how many bits of major/minor we use.  It just
// needs to be determined before we start serving.
//
// And for inode numbers, let's use as many lower bits as we can.  We can have
// up to 63 likely bits.  If only these bits are set, we can use the common case
// function.  If any of the unlikely bits are set, we'll have to use the map.
//
// Let's build the likely qid.path like so, with the partitions decided by
// the vars below, with bit63 = 0 for the Likely case.
//
// e.g. for the vars below, we'd have:
//
// 63 = 0
// |           51          39                                     0
// |-----------|-----------|--------------------------------------|
// |\---major--|\---minor--|\----------------inode----------------|

const devUpperBits = 32
const devUpperOffset = 32
const devMajorBits = 12
const devMinorBits = 20

// These can be tweaked by us at startup time.
var devMajorLikelyBits = devMajorBits - 0
var devMinorLikelyBits = devMinorBits - 8
var inodeLikelyBits = 39 // as much as we can get, up to 63 total

func init() {
	if devMajorLikelyBits+devMinorLikelyBits+
		inodeLikelyBits != 63 {
		panic("Bad dev-ino bit setup!")
	}
	nextQid.Store(uint64(1) << 63)
}

// Returns a u64 with the lower N bits set.
func nOnes(n int) uint64 {
	return (uint64(1) << n) - 1
}

// Returns the qid.path encoding for dev,ino and OK if it is likely.  To be
// "likely", dev and ino must only have the likely bits set.
func encodeLikely(dev, ino uint64) (uint64, bool) {
	inoLikely := nOnes(inodeLikelyBits)
	if (ino & ^inoLikely) != 0 {
		return 0, false
	}
	upperUnlikely := nOnes(devUpperBits) << devUpperOffset
	if (dev & upperUnlikely) != 0 {
		return 0, false
	}
	// In lieu of playing with bits, it suffices to make sure major and
	// minor fit within the bits that we'll encode in the "likely" case.
	major := uint64(unix.Major(dev))
	if major > nOnes(devMajorLikelyBits) {
		return 0, false
	}
	minor := uint64(unix.Minor(dev))
	if minor > nOnes(devMinorLikelyBits) {
		return 0, false
	}

	q := ino & inoLikely
	q |= minor << (inodeLikelyBits)
	q |= major << (inodeLikelyBits + devMinorLikelyBits)

	return q, true
}

type devino struct {
	dev uint64
	ino uint64
}

var qids sync.Map
var nextQid atomic.Uint64 // set to 1 << 63 in init()

func devInoToQid(dev, ino uint64) uint64 {
	if q, ok := encodeLikely(dev, ino); ok {
		return q
	}
	di := devino{dev, ino}
	if q, ok := qids.Load(di); ok {
		return q.(uint64)
	}
	// Could race and have two nextQids, but only one will win.  The other
	// will be ignored.  This is fine.
	q, _ := qids.LoadOrStore(di, nextQid.Add(1))
	return q.(uint64)
}

func statToQID(st *unix.Stat_t) p9.QID {
	return p9.QID{
		Type: p9.FileMode(st.Mode).QIDType(),
		Path: devInoToQid(uint64(st.Dev), st.Ino),
	}
}

func (l *Local) stat(st *unix.Stat_t) error {
	if l.file != nil {
		return withFileFD(l.file, func(fd int) error {
			return fstat(fd, st)
		})
	}
	dirFD, release, name, err := l.openTarget()
	if err != nil {
		return err
	}
	defer release()
	return fstatAt(dirFD, name, st, unix.AT_SYMLINK_NOFOLLOW)
}

func (l *Local) getAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	var st unix.Stat_t
	if err := l.stat(&st); err != nil {
		return p9.QID{}, p9.AttrMask{}, p9.Attr{}, err
	}
	return statToQID(&st), req, statToAttr(&st), nil
}

func (l *Local) walk(names []string) ([]p9.QID, p9.File, error) {
	curFD, releaseStart, err := l.openDir()
	if err != nil {
		return nil, nil, err
	}
	startFD := curFD
	defer func() {
		if curFD != startFD {
			_ = unix.Close(curFD)
		}
		releaseStart()
	}()

	qids := make([]p9.QID, 0, len(names))
	rel := l.rel
	for i, name := range names {
		var st unix.Stat_t
		if i < len(names)-1 {
			nextFD, err := openAt(curFD, name, openDirFlags|unix.O_NOFOLLOW, 0)
			if err != nil {
				return nil, nil, err
			}
			if curFD != startFD {
				_ = unix.Close(curFD)
			}
			curFD = nextFD
			if err := fstat(curFD, &st); err != nil {
				return nil, nil, err
			}
		} else {
			if err := fstatAt(curFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return nil, nil, err
			}
		}
		qids = append(qids, statToQID(&st))
		rel = joinRel(rel, name)
	}
	return qids, l.child(rel, nil, 0), nil
}

func (l *Local) open(mode p9.OpenFlags) (_ p9.QID, _ uint32, err error) {
	flags, err := filterOpenFlags(mode, unixAllowedOpenFlags)
	if err != nil {
		return p9.QID{}, 0, err
	}
	dirFD, release, name, err := l.openTarget()
	if err != nil {
		return p9.QID{}, 0, err
	}
	defer release()

	fd, err := openAt(dirFD, name, flags|unix.O_NOFOLLOW|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return p9.QID{}, 0, err
	}
	defer func() {
		if err != nil {
			_ = unix.Close(fd)
		}
	}()

	var st unix.Stat_t
	if err = fstat(fd, &st); err != nil {
		return p9.QID{}, 0, err
	}
	l.file = os.NewFile(uintptr(fd), l.displayName())
	l.mode = mode
	return statToQID(&st), 0, nil
}

func (l *Local) create(name string, mode p9.OpenFlags, permissions p9.FileMode) (_ p9.File, _ p9.QID, _ uint32, err error) {
	flags, err := filterOpenFlags(mode, unixAllowedOpenFlags)
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	dirFD, release, err := l.openDir()
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	defer release()

	openFlags := flags | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_NOCTTY | unix.O_CLOEXEC
	fd, err := openAt(dirFD, name, openFlags, uint32(permissions.Permissions()))
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	defer func() {
		if err != nil {
			_ = unix.Close(fd)
		}
	}()

	var st unix.Stat_t
	if err = fstat(fd, &st); err != nil {
		return nil, p9.QID{}, 0, err
	}

	newRel := joinRel(l.rel, name)
	l2 := l.child(newRel, os.NewFile(uintptr(fd), newRel), mode)
	return l2, statToQID(&st), 0, nil
}

func (l *Local) mkdir(name string, permissions p9.FileMode) error {
	dirFD, release, err := l.openDir()
	if err != nil {
		return err
	}
	defer release()

	return ignoringEINTR(func() error {
		return unix.Mkdirat(dirFD, name, uint32(permissions.Permissions()))
	})
}

func (l *Local) renameAt(oldName string, newDir *Local, newName string) error {
	oldDirFD, releaseOld, err := l.openDir()
	if err != nil {
		return err
	}
	defer releaseOld()

	newDirFD := oldDirFD
	if newDir.rel != l.rel {
		var releaseNew func()
		newDirFD, releaseNew, err = newDir.openDir()
		if err != nil {
			return err
		}
		defer releaseNew()
	}

	return ignoringEINTR(func() error {
		return unix.Renameat(oldDirFD, oldName, newDirFD, newName)
	})
}

func removeAt(dirFD int, name string) error {
	err := ignoringEINTR(func() error {
		return unix.Unlinkat(dirFD, name, 0)
	})
	if err == nil {
		return nil
	}
	errDir := ignoringEINTR(func() error {
		return unix.Unlinkat(dirFD, name, unix.AT_REMOVEDIR)
	})
	if errDir == nil {
		return nil
	}
	if errDir != unix.ENOTDIR {
		return errDir
	}
	return err
}

func (l *Local) remove() error {
	parentFD, release, base, err := l.openParent()
	if err != nil {
		return err
	}
	defer release()
	return removeAt(parentFD, base)
}

func (l *Local) unlinkAt(name string, flags uint32) error {
	dirFD, release, err := l.openDir()
	if err != nil {
		return err
	}
	defer release()

	if flags&linuxATRemoveDir != 0 {
		return ignoringEINTR(func() error {
			return unix.Unlinkat(dirFD, name, unix.AT_REMOVEDIR)
		})
	}
	// p9's Tremove calls UnlinkAt(name, 0) for both files and directories.
	return removeAt(dirFD, name)
}

// utimeOmitSpec returns the OS-specific UTIME_OMIT sentinel using untyped
// constants because darwin/netbsd omit unix.UTIME_OMIT in x/sys/unix and
// Timespec fields vary between int32 and int64 across architectures.
func utimeOmitSpec() unix.Timespec {
	switch runtime.GOOS {
	case "openbsd":
		return unix.Timespec{Sec: -0x1, Nsec: -0x1}
	case "netbsd", "linux", "android":
		return unix.Timespec{Sec: (1 << 30) - 2, Nsec: (1 << 30) - 2}
	default:
		// darwin, freebsd, solaris, illumos, dragonfly
		return unix.Timespec{Sec: -0x2, Nsec: -0x2}
	}
}

func buildUtimes(valid p9.SetAttrMask, attr p9.SetAttr) [2]unix.Timespec {
	omit := utimeOmitSpec()
	ts := [2]unix.Timespec{omit, omit}
	atime, mtime := resolveTimes(valid, attr)
	if valid.ATime {
		ts[0] = unix.NsecToTimespec(atime.UnixNano())
	}
	if valid.MTime {
		ts[1] = unix.NsecToTimespec(mtime.UnixNano())
	}
	return ts
}

func (l *Local) truncateSize(size int64, ensureTarget func() (int, string, error)) error {
	var want *unix.Stat_t
	if l.file != nil {
		if l.mode.Mode() != p9.ReadOnly {
			return l.file.Truncate(size)
		}
		// Fall back to path truncate if unsupported (ENOSYS) or /proc
		// is absent (ENOENT).
		if err := ftruncateReadOnly(l.file, size); err != unix.ENOSYS && err != unix.ENOENT {
			return err
		}
		var st unix.Stat_t
		if err := withFileFD(l.file, func(fd int) error {
			return fstat(fd, &st)
		}); err != nil {
			return err
		}
		want = &st
	}
	dirFD, target, err := ensureTarget()
	if err != nil {
		return err
	}
	return truncateAtNoFollow(dirFD, target, size, want)
}

func (l *Local) setTimes(valid p9.SetAttrMask, attr p9.SetAttr, ensureTarget func() (int, string, error)) error {
	ts := buildUtimes(valid, attr)
	if l.file != nil {
		// Fall back to UtimesNanoAt if unsupported (ENOSYS) or /proc
		// is absent (ENOENT).
		if err := futimesNano(l.file, &ts); err != unix.ENOSYS && err != unix.ENOENT {
			return err
		}
	}
	dirFD, name, err := ensureTarget()
	if err != nil {
		return err
	}
	if l.file != nil {
		if err := verifyTargetMatchesFile(l.file, dirFD, name); err != nil {
			return err
		}
	}
	return ignoringEINTR(func() error {
		return unix.UtimesNanoAt(dirFD, name, ts[:], unix.AT_SYMLINK_NOFOLLOW)
	})
}

func (l *Local) setAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	var (
		dirFD   int
		name    string
		release func()
	)
	defer func() {
		if release != nil {
			release()
		}
	}()
	ensureTarget := func() (int, string, error) {
		if release != nil {
			return dirFD, name, nil
		}
		var err error
		dirFD, release, name, err = l.openTarget()
		return dirFD, name, err
	}

	// Perform Size, ATime/MTime, and UID/GID before Permissions so that:
	// 1. A single SetAttr call that both modifies size/timestamps and
	//    removes write permissions (e.g., chmod 0) does not fail its own
	//    truncate or chtimes.
	// 2. Chmod runs last so setuid/setgid bits are not cleared by truncate
	//    or chown.
	if valid.Size {
		if err := l.truncateSize(int64(attr.Size), ensureTarget); err != nil {
			return err
		}
	}
	if valid.ATime || valid.MTime {
		if err := l.setTimes(valid, attr, ensureTarget); err != nil {
			return err
		}
	}
	if valid.UID || valid.GID {
		uid, gid := -1, -1
		if valid.UID {
			uid = int(attr.UID)
		}
		if valid.GID {
			gid = int(attr.GID)
		}
		if l.file != nil {
			if err := withFileFD(l.file, func(fd int) error {
				return ignoringEINTR(func() error {
					return unix.Fchown(fd, uid, gid)
				})
			}); err != nil {
				return err
			}
		} else {
			fd, target, err := ensureTarget()
			if err != nil {
				return err
			}
			if err := ignoringEINTR(func() error {
				return unix.Fchownat(fd, target, uid, gid, unix.AT_SYMLINK_NOFOLLOW)
			}); err != nil {
				return err
			}
		}
	}
	if valid.Permissions {
		mode := uint32(attr.Permissions.Permissions())
		if l.file != nil {
			if err := withFileFD(l.file, func(fd int) error {
				return ignoringEINTR(func() error {
					return unix.Fchmod(fd, mode)
				})
			}); err != nil {
				return err
			}
		} else {
			fd, target, err := ensureTarget()
			if err != nil {
				return err
			}
			if err := fchmodAtNoFollow(fd, target, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Local) openDirForRead() (int, error) {
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOCTTY | unix.O_CLOEXEC
	if l.file != nil {
		// Open a fresh description of the same directory inode so
		// Readdir starts at offset 0 without mutating l.file's offset.
		var fd int
		err := withFileFD(l.file, func(openFD int) error {
			var openErr error
			fd, openErr = openAt(openFD, ".", flags, 0)
			return openErr
		})
		return fd, err
	}
	dirFD, release, name, err := l.openTarget()
	if err != nil {
		return -1, err
	}
	defer release()
	return openAt(dirFD, name, flags|unix.O_NOFOLLOW, 0)
}

func (l *Local) readDir(offset uint64, count uint32) (p9.Dirents, error) {
	fd, err := l.openDirForRead()
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), l.displayName())
	defer f.Close()

	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	if offset >= uint64(len(names)) {
		return nil, nil
	}

	var dirents p9.Dirents
	err = withFileFD(f, func(dirFD int) error {
		for i, name := range names[offset:] {
			if uint64(len(dirents)) >= uint64(count) {
				break
			}
			var st unix.Stat_t
			if err := fstatAt(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				continue
			}
			qid := statToQID(&st)
			dirents = append(dirents, p9.Dirent{
				QID:    qid,
				Type:   qid.Type,
				Name:   name,
				Offset: offset + uint64(i) + 1,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dirents, nil
}

// lock implements p9.File.Lock.
func (l *Local) lock(pid int, locktype p9.LockType, flags p9.LockFlags, start, length uint64, client string) (p9.LockStatus, error) {
	if l.file == nil {
		return p9.LockStatusError, unix.EBADF
	}
	var cmd int
	switch flags {
	case p9.LockFlagsBlock:
		cmd = unix.F_SETLKW
	case p9.LockFlagsReclaim:
		return p9.LockStatusError, unix.ENOSYS
	default:
		cmd = unix.F_SETLK
	}
	var t int16
	switch locktype {
	case p9.ReadLock:
		t = unix.F_RDLCK
	case p9.WriteLock:
		t = unix.F_WRLCK
	case p9.Unlock:
		t = unix.F_UNLCK
	default:
		return p9.LockStatusError, unix.ENOSYS
	}
	lk := &unix.Flock_t{
		Type:   t,
		Whence: io.SeekStart,
		Start:  int64(start),
		Len:    int64(length),
	}
	if err := withFileFD(l.file, func(fd int) error {
		return unix.FcntlFlock(uintptr(fd), cmd, lk)
	}); err != nil {
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EACCES) {
			return p9.LockStatusBlocked, nil
		}
		return p9.LockStatusError, err
	}
	return p9.LockStatusOK, nil
}
