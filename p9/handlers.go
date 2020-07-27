// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package p9

import (
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/hugelgupf/p9/internal"
	"github.com/hugelgupf/p9/internal/linux"
)

// cs.session.newErr returns a new error message from an error.
func (s *session) newErr(err error) message {
	switch s.baseVersion {
	case version9P2000L:
		return &rlerror{Error: uint32(internal.ExtractErrno(err))}

	default:
		fallthrough
	case version9P2000:
		return &rerror{err: err.Error()}
	}
}

// handler is implemented for server-handled messages.
//
// See server.go for call information.
type handler interface {
	// Handle handles the given message.
	//
	// This may modify the server state. The handle function must return a
	// message which will be sent back to the client. It may be useful to
	// use cs.session.newErr to automatically extract an error message.
	handle(cs *connState) message
}

// handle implements handler.handle.
func (t *tversion) handle(cs *connState) message {
	// "If the server does not understand the client's version string, it
	// should respond with an Rversion message (not Rerror) with the
	// version string the 7 characters "unknown"".
	//
	// - 9P2000 spec.
	//
	// Makes sense, since there are two different kinds of errors depending on the version.
	unknown := &rversion{
		MSize:   0,
		Version: "unknown",
	}
	if t.MSize == 0 {
		return unknown
	}
	msize := t.MSize
	if t.MSize > maximumLength {
		msize = maximumLength
	}

	reqBaseVersion, reqVersion, ok := parseVersion(t.Version)
	if !ok {
		return unknown
	}
	s := &session{
		fids:        make(map[fid]*fidRef),
		tags:        make(map[tag]chan struct{}),
		recvOkay:    make(chan bool),
		messageSize: msize,
	}
	switch reqBaseVersion {
	case version9P2000U:
		return unknown

	case version9P2000:
		s.baseVersion = version9P2000
		s.msgRegistry = &msg9P2000Registry

	case version9P2000L:
		s.baseVersion = version9P2000L
		s.msgRegistry = &msgDotLRegistry

		// The server cannot support newer versions that it doesn't know about.  In this
		// case we return EAGAIN to tell the client to try again with a lower version.
		//
		// From Tversion(9P): "The server may respond with the client’s version
		// string, or a version string identifying an earlier defined protocol version".
		if reqVersion > highestSupportedVersion {
			s.version = highestSupportedVersion
		} else {
			s.version = reqVersion
		}
	}

	// Clunk all FIDs. Make sure we stop recv using this session.
	//
	// This is safe, because there should be no new messages received at
	// this point, because tversion defers recvDone in handleRequest.
	//
	// TODO: What is still NOT SAFE is, we should be waiting for all
	// outstanding handlers to complete as well. I.e. pending on
	// handleRequest should go to 0 before we do this.
	//
	// What we really should be doing is sequencing this in handleRequest
	// somehow. Stay tuned.
	cs.session.stop()

	// Nothing else should be trying to recv right now. No need to lock.
	cs.session = s

	return &rversion{
		MSize:   s.messageSize,
		Version: versionString(s.baseVersion, s.version),
	}
}

// handle implements handler.handle.
func (t *tflush) handle(cs *connState) message {
	cs.session.WaitTag(t.OldTag)
	return &rflush{}
}

// checkSafeName validates the name and returns nil or returns an error.
func checkSafeName(name string) error {
	if name != "" && !strings.Contains(name, "/") && name != "." && name != ".." {
		return nil
	}
	return linux.EINVAL
}

// handle implements handler.handle.
func (t *tclunk) handle(cs *connState) message {
	if !cs.session.DeleteFID(t.fid) {
		return cs.session.newErr(linux.EBADF)
	}
	return &rclunk{}
}

// handle implements handler.handle.
func (t *tremove) handle(cs *connState) message {
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// Frustratingly, because we can't be guaranteed that a rename is not
	// occurring simultaneously with this removal, we need to acquire the
	// global rename lock for this kind of remove operation to ensure that
	// ref.parent does not change out from underneath us.
	//
	// This is why Tremove is a bad idea, and clients should generally use
	// Tunlinkat. All p9 clients will use Tunlinkat.
	err := ref.safelyGlobal(func() error {
		// Is this a root? Can't remove that.
		if ref.isRoot() {
			return linux.EINVAL
		}

		// N.B. this remove operation is permitted, even if the file is open.
		// See also rename below for reasoning.

		// Is this file already deleted?
		if ref.isDeleted() {
			return linux.EINVAL
		}

		// Retrieve the file's proper name.
		name := ref.parent.pathNode.nameFor(ref)

		var err error
		if ref.file != nil {
			// Attempt the removal.
			err = ref.parent.file.UnlinkAt(name, 0)
		} else {
			err = ref.legacyFile.Remove()
		}
		if err != nil {
			return err
		}

		// Mark all relevant fids as deleted. We don't need to lock any
		// individual nodes because we already hold the global lock.
		ref.parent.markChildDeleted(name)
		return nil
	})

	// "The remove request asks the file server both to remove the file
	// represented by fid and to clunk the fid, even if the remove fails."
	//
	// "It is correct to consider remove to be a clunk with the side effect
	// of removing the file if permissions allow."
	// https://swtch.com/plan9port/man/man9/remove.html
	if !cs.session.DeleteFID(t.fid) {
		return cs.session.newErr(linux.EBADF)
	}
	if err != nil {
		return cs.session.newErr(err)
	}

	return &rremove{}
}

// handle implements handler.handle.
//
// We don't support authentication, so this just returns ENOSYS.
func (t *tauth) handle(cs *connState) message {
	return cs.session.newErr(linux.ENOSYS)
}

// handle implements handler.handle for the 9P2000.L Tattach.
func (t *tlattach) handle(cs *connState) message {
	// Ensure no authentication fid is provided.
	if t.Auth.Authenticationfid != noFID {
		return cs.session.newErr(linux.EINVAL)
	}

	// Must provide an absolute path.
	if path.IsAbs(t.Auth.AttachName) {
		// Trim off the leading / if the path is absolute. We always
		// treat attach paths as absolute and call attach with the root
		// argument on the server file for clarity.
		t.Auth.AttachName = t.Auth.AttachName[1:]
	}

	// Do the attach on the root.
	sf, err := cs.server.attacher.Attach()
	if err != nil {
		return cs.session.newErr(err)
	}
	qid, valid, attr, err := sf.GetAttr(AttrMaskAll)
	if err != nil {
		sf.Close() // Drop file.
		return cs.session.newErr(err)
	}
	if !valid.Mode {
		sf.Close() // Drop file.
		return cs.session.newErr(linux.EINVAL)
	}

	// Build a transient reference.
	root := &fidRef{
		server:     cs.server,
		parent:     nil,
		file:       sf,
		refs:       1,
		isDir:      attr.Mode.FileType().IsDir(),
		isOpenable: CanOpen(attr.Mode.FileType()),
		pathNode:   cs.server.pathTree,
	}
	defer root.DecRef()

	// Attach the root?
	if len(t.Auth.AttachName) == 0 {
		cs.session.InsertFID(t.fid, root)
		return &rattach{QID: qid}
	}

	// We want the same traversal checks to apply on attach, so always
	// attach at the root and use the regular walk paths.
	names := strings.Split(t.Auth.AttachName, "/")
	_, newRef, err := doWalk(cs, root, names, func(from *fidRef, names []string) ([]QID, *fidRef, error) {
		qids, newRef, _, _, err := walkOneLinux(from, names, false)
		return qids, newRef, err
	})
	if err != nil {
		return cs.session.newErr(err)
	}
	defer newRef.DecRef()

	// Insert the fid.
	cs.session.InsertFID(t.fid, newRef)
	return &rattach{QID: qid}
}

// handle implements handler.handle.
func (t *tattach) handle(cs *connState) message {
	// Ensure no authentication fid is provided.
	if t.Auth.Authenticationfid != noFID {
		return cs.session.newErr(linux.EINVAL)
	}

	// Must provide an absolute path.
	if path.IsAbs(t.Auth.AttachName) {
		// Trim off the leading / if the path is absolute. We always
		// treat attach paths as absolute and call attach with the root
		// argument on the server file for clarity.
		t.Auth.AttachName = t.Auth.AttachName[1:]
	}

	// Do the attach on the root.
	qid, sf, err := cs.server.legacyAttacher.Attach()
	if err != nil {
		return cs.session.newErr(err)
	}
	s, err := sf.Stat()
	if err != nil {
		sf.Close() // Drop file.
		return cs.session.newErr(err)
	}

	// Build a transient reference.
	root := &fidRef{
		server:     cs.server,
		parent:     nil,
		legacyFile: sf,
		refs:       1,
		isDir:      s.Mode&DMDIR == DMDIR,
		isOpenable: true,
		pathNode:   cs.server.pathTree,
	}
	defer root.DecRef()

	// Attach the root?
	if len(t.Auth.AttachName) == 0 {
		cs.session.InsertFID(t.fid, root)
		return &rattach{QID: qid}
	}

	// We want the same traversal checks to apply on attach, so always
	// attach at the root and use the regular walk paths.
	names := strings.Split(t.Auth.AttachName, "/")
	_, newRef, err := doWalk(cs, root, names, walkOneLegacy)
	if err != nil {
		return cs.session.newErr(err)
	}
	defer newRef.DecRef()

	// Insert the fid.
	cs.session.InsertFID(t.fid, newRef)
	return &rattach{QID: qid}
}

// CanOpen returns whether this file open can be opened, read and written to.
//
// This includes everything except symlinks and sockets.
func CanOpen(mode FileMode) bool {
	return mode.IsRegular() || mode.IsDir() || mode.IsNamedPipe() || mode.IsBlockDevice() || mode.IsCharacterDevice()
}

// handle implements handler.handle for the 9P2000.L Tlopen.
func (t *tlopen) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	ref.openedMu.Lock()
	defer ref.openedMu.Unlock()

	// Has it been opened already?
	if ref.opened || !ref.isOpenable {
		return cs.session.newErr(linux.EINVAL)
	}

	// Is this an attempt to open a directory as writable? Don't accept.
	if ref.isDir && t.Flags.Mode() != ReadOnly {
		return cs.session.newErr(linux.EINVAL)
	}

	var (
		qid    QID
		ioUnit uint32
	)
	if err := ref.safelyRead(func() (err error) {
		// Don't allow readlink on deleted files.
		if ref.isDeleted() {
			return linux.EINVAL
		}

		// Do the open.
		qid, ioUnit, err = ref.file.Open(t.Flags)
		return err
	}); err != nil {
		return cs.session.newErr(err)
	}

	// Mark file as opened and set open mode.
	ref.opened = true
	ref.openFlags = t.Flags

	return &rlopen{QID: qid, IoUnit: ioUnit}
}

// handle implements handler.handle for the 9P2000 Topen.
func (t *topen) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	ref.openedMu.Lock()
	defer ref.openedMu.Unlock()

	// Has it been opened already?
	if ref.opened || !ref.isOpenable {
		return cs.session.newErr(linux.EINVAL)
	}

	// Is this an attempt to open a directory as writable? Don't accept.
	if ref.isDir && t.mode.Mode() != OREAD && t.mode.Mode() != OEXEC {
		return cs.session.newErr(linux.EINVAL)
	}

	var (
		qid    QID
		ioUnit uint32
	)
	if err := ref.safelyRead(func() (err error) {
		// Has it been deleted already?
		if ref.isDeleted() {
			return linux.EINVAL
		}

		// Do the open.
		qid, ioUnit, err = ref.legacyFile.Open(t.mode)
		return err
	}); err != nil {
		return cs.session.newErr(err)
	}

	// Mark file as opened and set open mode.
	ref.opened = true
	ref.openFlags = t.mode.OpenFlags()

	return &ropen{QID: qid, IoUnit: ioUnit}
}

func (t *tcreate) do(cs *connState, uid UID) (*rcreate, error) {
	// Don't allow complex names.
	if err := checkSafeName(t.Name); err != nil {
		return nil, err
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return nil, linux.EBADF
	}
	defer ref.DecRef()

	var (
		nsf    LegacyFile
		qid    QID
		ioUnit uint32
		newRef *fidRef
	)
	if err := ref.safelyWrite(func() (err error) {
		// Don't allow creation from non-directories or deleted directories.
		if ref.isDeleted() || !ref.isDir {
			return linux.EINVAL
		}

		// Not allowed on open directories.
		if _, opened := ref.OpenFlags(); opened {
			return linux.EINVAL
		}

		// Do the create.
		nsf, qid, ioUnit, err = ref.legacyFile.Create(t.Name, t.Mode, t.Permissions)
		if err != nil {
			return err
		}

		newRef = &fidRef{
			server:     cs.server,
			parent:     ref,
			legacyFile: nsf,
			opened:     true,
			openFlags:  t.Mode.OpenFlags(),
			isDir:      t.Permissions&DMDIR == DMDIR,
			isOpenable: true,
			pathNode:   ref.pathNode.pathNodeFor(t.Name),
		}
		ref.pathNode.addChild(newRef, t.Name)
		ref.IncRef() // Acquire parent reference.
		return nil
	}); err != nil {
		return nil, err
	}

	// Replace the fid reference.
	cs.session.InsertFID(t.fid, newRef)

	return &rcreate{ropen: ropen{QID: qid, IoUnit: ioUnit}}, nil
}

func (t *tlcreate) do(cs *connState, uid UID) (*rlcreate, error) {
	// Don't allow complex names.
	if err := checkSafeName(t.Name); err != nil {
		return nil, err
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return nil, linux.EBADF
	}
	defer ref.DecRef()

	var (
		nsf    File
		qid    QID
		ioUnit uint32
		newRef *fidRef
	)
	if err := ref.safelyWrite(func() (err error) {
		// Don't allow creation from non-directories or deleted directories.
		if ref.isDeleted() || !ref.isDir {
			return linux.EINVAL
		}

		// Not allowed on open directories.
		if _, opened := ref.OpenFlags(); opened {
			return linux.EINVAL
		}

		// Do the create.
		nsf, qid, ioUnit, err = ref.file.Create(t.Name, t.OpenFlags, t.Permissions, uid, t.GID)
		if err != nil {
			return err
		}

		newRef = &fidRef{
			server:     cs.server,
			parent:     ref,
			file:       nsf,
			opened:     true,
			openFlags:  t.OpenFlags,
			isDir:      false,
			isOpenable: true,
			pathNode:   ref.pathNode.pathNodeFor(t.Name),
		}
		ref.pathNode.addChild(newRef, t.Name)
		ref.IncRef() // Acquire parent reference.
		return nil
	}); err != nil {
		return nil, err
	}

	// Replace the fid reference.
	cs.session.InsertFID(t.fid, newRef)

	return &rlcreate{rlopen: rlopen{QID: qid, IoUnit: ioUnit}}, nil
}

// handle implements handler.handle.
func (t *tlcreate) handle(cs *connState) message {
	rlcreate, err := t.do(cs, NoUID)
	if err != nil {
		return cs.session.newErr(err)
	}
	return rlcreate
}

// handle implements handler.handle.
func (t *tsymlink) handle(cs *connState) message {
	rsymlink, err := t.do(cs, NoUID)
	if err != nil {
		return cs.session.newErr(err)
	}
	return rsymlink
}

func (t *tsymlink) do(cs *connState, uid UID) (*rsymlink, error) {
	// Don't allow complex names.
	if err := checkSafeName(t.Name); err != nil {
		return nil, err
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.Directory)
	if !ok {
		return nil, linux.EBADF
	}
	defer ref.DecRef()

	var qid QID
	if err := ref.safelyWrite(func() (err error) {
		// Don't allow symlinks from non-directories or deleted directories.
		if ref.isDeleted() || !ref.isDir {
			return linux.EINVAL
		}

		// Not allowed on open directories.
		if _, opened := ref.OpenFlags(); opened {
			return linux.EINVAL
		}

		// Do the symlink.
		qid, err = ref.file.Symlink(t.Target, t.Name, uid, t.GID)
		return err
	}); err != nil {
		return nil, err
	}

	return &rsymlink{QID: qid}, nil
}

// handle implements handler.handle.
func (t *tlink) handle(cs *connState) message {
	// Don't allow complex names.
	if err := checkSafeName(t.Name); err != nil {
		return cs.session.newErr(err)
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.Directory)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// Lookup the other fid.
	refTarget, ok := cs.session.LookupFID(t.Target)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer refTarget.DecRef()

	if err := ref.safelyWrite(func() (err error) {
		// Don't allow create links from non-directories or deleted directories.
		if ref.isDeleted() || !ref.isDir {
			return linux.EINVAL
		}

		// Not allowed on open directories.
		if _, opened := ref.OpenFlags(); opened {
			return linux.EINVAL
		}

		// Do the link.
		return ref.file.Link(refTarget.file, t.Name)
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rlink{}
}

// handle implements handler.handle.
func (t *trenameat) handle(cs *connState) message {
	// Don't allow complex names.
	if err := checkSafeName(t.OldName); err != nil {
		return cs.session.newErr(err)
	}
	if err := checkSafeName(t.NewName); err != nil {
		return cs.session.newErr(err)
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.OldDirectory)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// Lookup the other fid.
	refTarget, ok := cs.session.LookupFID(t.NewDirectory)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer refTarget.DecRef()

	// Perform the rename holding the global lock.
	if err := ref.safelyGlobal(func() (err error) {
		// Don't allow renaming across deleted directories.
		if ref.isDeleted() || !ref.isDir || refTarget.isDeleted() || !refTarget.isDir {
			return linux.EINVAL
		}

		// Not allowed on open directories.
		if _, opened := ref.OpenFlags(); opened {
			return linux.EINVAL
		}

		// Is this the same file? If yes, short-circuit and return success.
		if ref.pathNode == refTarget.pathNode && t.OldName == t.NewName {
			return nil
		}

		// Attempt the actual rename.
		if err := ref.file.RenameAt(t.OldName, refTarget.file, t.NewName); err != nil {
			return err
		}

		// Update the path tree.
		ref.renameChildTo(t.OldName, refTarget, t.NewName)
		return nil
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rrenameat{}
}

// handle implements handler.handle.
func (t *tunlinkat) handle(cs *connState) message {
	// Don't allow complex names.
	if err := checkSafeName(t.Name); err != nil {
		return cs.session.newErr(err)
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.Directory)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	if err := ref.safelyWrite(func() (err error) {
		// Don't allow deletion from non-directories or deleted directories.
		if ref.isDeleted() || !ref.isDir {
			return linux.EINVAL
		}

		// Not allowed on open directories.
		if _, opened := ref.OpenFlags(); opened {
			return linux.EINVAL
		}

		// Before we do the unlink itself, we need to ensure that there
		// are no operations in flight on associated path node. The
		// child's path node lock must be held to ensure that the
		// unlinkat marking the child deleted below is atomic with
		// respect to any other read or write operations.
		//
		// This is one case where we have a lock ordering issue, but
		// since we always acquire deeper in the hierarchy, we know
		// that we are free of lock cycles.
		childPathNode := ref.pathNode.pathNodeFor(t.Name)
		childPathNode.opMu.Lock()
		defer childPathNode.opMu.Unlock()

		// Do the unlink.
		err = ref.file.UnlinkAt(t.Name, t.Flags)
		if err != nil {
			return err
		}

		// Mark the path as deleted.
		ref.markChildDeleted(t.Name)
		return nil
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &runlinkat{}
}

// handle implements handler.handle.
func (t *trename) handle(cs *connState) message {
	// Don't allow complex names.
	if err := checkSafeName(t.Name); err != nil {
		return cs.session.newErr(err)
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// Lookup the target.
	refTarget, ok := cs.session.LookupFID(t.Directory)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer refTarget.DecRef()

	if err := ref.safelyGlobal(func() (err error) {
		// Don't allow a root rename.
		if ref.isRoot() {
			return linux.EINVAL
		}

		// Don't allow renaming deleting entries, or target non-directories.
		if ref.isDeleted() || refTarget.isDeleted() || !refTarget.isDir {
			return linux.EINVAL
		}

		// If the parent is deleted, but we not, something is seriously wrong.
		// It's fail to die at this point with an assertion failure.
		if ref.parent.isDeleted() {
			panic(fmt.Sprintf("parent %+v deleted, child %+v is not", ref.parent, ref))
		}

		// N.B. The rename operation is allowed to proceed on open files. It
		// does impact the state of its parent, but this is merely a sanity
		// check in any case, and the operation is safe. There may be other
		// files corresponding to the same path that are renamed anyways.

		// Check for the exact same file and short-circuit.
		oldName := ref.parent.pathNode.nameFor(ref)
		if ref.parent.pathNode == refTarget.pathNode && oldName == t.Name {
			return nil
		}

		// Call the rename method on the parent.
		if err := ref.parent.file.RenameAt(oldName, refTarget.file, t.Name); err != nil {
			return err
		}

		// Update the path tree.
		ref.parent.renameChildTo(oldName, refTarget, t.Name)
		return nil
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rrename{}
}

// handle implements handler.handle.
func (t *treadlink) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	var target string
	if err := ref.safelyRead(func() (err error) {
		// Don't allow readlink on deleted files.
		if ref.isDeleted() {
			return linux.EINVAL
		}

		// Do the read.
		target, err = ref.file.Readlink()
		return err
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rreadlink{target}
}

// handle implements handler.handle.
func (t *tread) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// Constrain the size of the read buffer.
	if int(t.Count) > int(maximumLength) {
		return cs.session.newErr(linux.ENOBUFS)
	}

	var (
		data = make([]byte, t.Count)
		n    int
	)
	if err := ref.safelyRead(func() (err error) {
		// Has it been opened already?
		openFlags, opened := ref.OpenFlags()
		if !opened {
			return linux.EINVAL
		}

		// Can it be read? Check permissions.
		if openFlags.Mode() == WriteOnly {
			return linux.EPERM
		}

		if ref.file != nil {
			n, err = ref.file.ReadAt(data, int64(t.Offset))
		} else {
			n, err = ref.legacyFile.ReadAt(data, int64(t.Offset))
		}
		return err
	}); err != nil && err != io.EOF {
		return cs.session.newErr(err)
	}

	return &rread{Data: data[:n]}
}

// handle implements handler.handle.
func (t *twrite) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	var n int
	if err := ref.safelyRead(func() (err error) {
		// Has it been opened already?
		openFlags, opened := ref.OpenFlags()
		if !opened {
			return linux.EINVAL
		}

		mode := openFlags.Mode()
		// Can it be written? Check permissions.
		if mode == ReadOnly || mode == ReadAndExecute {
			return linux.EPERM
		}

		if ref.file != nil {
			n, err = ref.file.WriteAt(t.Data, int64(t.Offset))
		} else {
			n, err = ref.legacyFile.WriteAt(t.Data, int64(t.Offset))
		}
		return err
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rwrite{Count: uint32(n)}
}

// handle implements handler.handle.
func (t *tmknod) handle(cs *connState) message {
	rmknod, err := t.do(cs, NoUID)
	if err != nil {
		return cs.session.newErr(err)
	}
	return rmknod
}

func (t *tmknod) do(cs *connState, uid UID) (*rmknod, error) {
	// Don't allow complex names.
	if err := checkSafeName(t.Name); err != nil {
		return nil, err
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.Directory)
	if !ok {
		return nil, linux.EBADF
	}
	defer ref.DecRef()

	var qid QID
	if err := ref.safelyWrite(func() (err error) {
		// Don't allow mknod on deleted files.
		if ref.isDeleted() || !ref.isDir {
			return linux.EINVAL
		}

		// Not allowed on open directories.
		if _, opened := ref.OpenFlags(); opened {
			return linux.EINVAL
		}

		// Do the mknod.
		qid, err = ref.file.Mknod(t.Name, t.Mode, t.Major, t.Minor, uid, t.GID)
		return err
	}); err != nil {
		return nil, err
	}

	return &rmknod{QID: qid}, nil
}

// handle implements handler.handle.
func (t *tmkdir) handle(cs *connState) message {
	rmkdir, err := t.do(cs, NoUID)
	if err != nil {
		return cs.session.newErr(err)
	}
	return rmkdir
}

func (t *tmkdir) do(cs *connState, uid UID) (*rmkdir, error) {
	// Don't allow complex names.
	if err := checkSafeName(t.Name); err != nil {
		return nil, err
	}

	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.Directory)
	if !ok {
		return nil, linux.EBADF
	}
	defer ref.DecRef()

	var qid QID
	if err := ref.safelyWrite(func() (err error) {
		// Don't allow mkdir on deleted files.
		if ref.isDeleted() || !ref.isDir {
			return linux.EINVAL
		}

		// Not allowed on open directories.
		if _, opened := ref.OpenFlags(); opened {
			return linux.EINVAL
		}

		// Do the mkdir.
		qid, err = ref.file.Mkdir(t.Name, t.Permissions, uid, t.GID)
		return err
	}); err != nil {
		return nil, err
	}

	return &rmkdir{QID: qid}, nil
}

// handle implements handler.handle.
func (t *tgetattr) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// We allow getattr on deleted files. Depending on the backing
	// implementation, it's possible that races exist that might allow
	// fetching attributes of other files. But we need to generally allow
	// refreshing attributes and this is a minor leak, if at all.

	var (
		qid   QID
		valid AttrMask
		attr  Attr
	)
	if err := ref.safelyRead(func() (err error) {
		qid, valid, attr, err = ref.file.GetAttr(t.AttrMask)
		return err
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rgetattr{QID: qid, Valid: valid, Attr: attr}
}

// handle implements handler.handle.
func (t *tsetattr) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	if err := ref.safelyWrite(func() error {
		// We don't allow setattr on files that have been deleted.
		// This might be technically incorrect, as it's possible that
		// there were multiple links and you can still change the
		// corresponding inode information.
		if ref.isDeleted() {
			return linux.EINVAL
		}

		// Set the attributes.
		return ref.file.SetAttr(t.Valid, t.SetAttr)
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rsetattr{}
}

// handle implements handler.handle.
func (t *txattrwalk) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// We don't support extended attributes.
	return cs.session.newErr(linux.ENODATA)
}

// handle implements handler.handle.
func (t *txattrcreate) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// We don't support extended attributes.
	return cs.session.newErr(linux.ENOSYS)
}

// handle implements handler.handle.
func (t *treaddir) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.Directory)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	var entries []Dirent
	if err := ref.safelyRead(func() (err error) {
		// Don't allow reading deleted directories.
		if ref.isDeleted() || !ref.isDir {
			return linux.EINVAL
		}

		// Has it been opened already?
		if _, opened := ref.OpenFlags(); !opened {
			return linux.EINVAL
		}

		// Read the entries.
		entries, err = ref.file.Readdir(t.Offset, t.Count)
		if err != nil && err != io.EOF {
			return err
		}
		return nil
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rreaddir{Count: t.Count, Entries: entries}
}

// handle implements handler.handle.
func (t *tfsync) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	if err := ref.safelyRead(func() (err error) {
		// Has it been opened already?
		if _, opened := ref.OpenFlags(); !opened {
			return linux.EINVAL
		}

		// Perform the sync.
		return ref.file.FSync()
	}); err != nil {
		return cs.session.newErr(err)
	}

	return &rfsync{}
}

// handle implements handler.handle.
func (t *tstatfs) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	st, err := ref.file.StatFS()
	if err != nil {
		return cs.session.newErr(err)
	}

	return &rstatfs{st}
}

// walkOneLegacy walks zero or one path elements in 9P2000.
//
// The slice passed as qids is append and returned.
func walkOneLegacy(from *fidRef, names []string) ([]QID, *fidRef, error) {
	if len(names) > 1 {
		// We require exactly zero or one elements.
		return nil, nil, linux.EINVAL
	}
	localQIDs, sf, err := from.legacyFile.Walk(names)
	if err != nil {
		// Error walking, don't return anything.
		return nil, nil, err
	}
	var s Stat
	if len(names) == 1 {
		s, err = sf.Stat()
		if err != nil {
			// Don't leak the file.
			sf.Close()
			return nil, nil, err
		}
	}
	if len(localQIDs) != 1 {
		// Expected a single QID.
		sf.Close()
		return nil, nil, linux.EINVAL
	}

	var newRef *fidRef
	switch len(names) {
	case 0:
		newRef = &fidRef{
			server:     from.server,
			legacyFile: sf,
			parent:     from.parent,
			isDir:      from.isDir,
			isOpenable: from.isOpenable,
			pathNode:   from.pathNode,

			// For the clone case, the cloned fid must
			// preserve the deleted property of the
			// original fid.
			deleted: from.deleted,
		}

	case 1:
		// Note that we don't need to acquire a lock on any of
		// these individual instances. That's because they are
		// not actually addressable via a fid. They are
		// anonymous. They exist in the tree for tracking
		// purposes.
		newRef = &fidRef{
			server:     from.server,
			legacyFile: sf,
			parent:     from,
			isDir:      s.Mode&DMDIR == DMDIR,
			isOpenable: true,
			pathNode:   from.pathNode.pathNodeFor(names[0]),
		}
	}
	return localQIDs, newRef, nil
}

// walkOneLinux walks zero or one path elements in 9P2000.L.
//
// The slice passed as qids is append and returned.
func walkOneLinux(from *fidRef, names []string, getattr bool) ([]QID, *fidRef, AttrMask, Attr, error) {
	if len(names) > 1 {
		// We require exactly zero or one elements.
		return nil, nil, AttrMask{}, Attr{}, linux.EINVAL
	}
	var (
		localQIDs []QID
		sf        File
		valid     AttrMask
		attr      Attr
		err       error
	)
	switch {
	case getattr:
		localQIDs, sf, valid, attr, err = from.file.WalkGetAttr(names)
		// Can't put fallthrough in the if because Go.
		if err != linux.ENOSYS {
			break
		}
		fallthrough
	default:
		localQIDs, sf, err = from.file.Walk(names)
		if err != nil {
			// No way to walk this element.
			break
		}
		if getattr || len(names) == 1 {
			_, valid, attr, err = sf.GetAttr(AttrMaskAll)
			if err != nil {
				// Don't leak the file.
				sf.Close()
			}
		}
	}
	if err != nil {
		// Error walking, don't return anything.
		return nil, nil, AttrMask{}, Attr{}, err
	}
	if len(localQIDs) != 1 {
		// Expected a single QID.
		sf.Close()
		return nil, nil, AttrMask{}, Attr{}, linux.EINVAL
	}

	var newRef *fidRef
	switch len(names) {
	case 0:
		newRef = &fidRef{
			server:     from.server,
			file:       sf,
			parent:     from.parent,
			isDir:      from.isDir,
			isOpenable: from.isOpenable,
			pathNode:   from.pathNode,

			// For the clone case, the cloned fid must
			// preserve the deleted property of the
			// original fid.
			deleted: from.deleted,
		}

	case 1:
		// Note that we don't need to acquire a lock on any of
		// these individual instances. That's because they are
		// not actually addressable via a fid. They are
		// anonymous. They exist in the tree for tracking
		// purposes.
		newRef = &fidRef{
			server:     from.server,
			file:       sf,
			parent:     from,
			isDir:      attr.Mode.FileType().IsDir(),
			isOpenable: CanOpen(attr.Mode.FileType()),
			pathNode:   from.pathNode.pathNodeFor(names[0]),
		}
	}
	return localQIDs, newRef, valid, attr, nil
}

type walkOneFunc func(from *fidRef, names []string) ([]QID, *fidRef, error)

// doWalk walks from a given fidRef.
//
// This enforces that all intermediate nodes are walkable (directories). The
// fidRef returned (newRef) has a reference associated with it that is now
// owned by the caller and must be handled appropriately.
func doWalk(cs *connState, ref *fidRef, names []string, walkOne walkOneFunc) (qids []QID, newRef *fidRef, err error) {
	// Check the names.
	for _, name := range names {
		err = checkSafeName(name)
		if err != nil {
			return
		}
	}

	// Has it been opened already?
	if _, opened := ref.OpenFlags(); opened {
		err = linux.EBUSY
		return
	}

	// Is this an empty list? Handle specially. We don't actually need to
	// validate anything since this is always permitted.
	if len(names) == 0 {
		if err := ref.maybeParent().safelyRead(func() (err error) {
			var localQIDs []QID
			localQIDs, newRef, err = walkOne(ref, nil)
			if err != nil {
				return err
			}
			qids = append(qids, localQIDs...)

			if !ref.isRoot() {
				if !newRef.isDeleted() {
					// Add only if a non-root node; the same node.
					ref.parent.pathNode.addChild(newRef, ref.parent.pathNode.nameFor(ref))
				}
				ref.parent.IncRef() // Acquire parent reference.
			}
			// doWalk returns a reference.
			newRef.IncRef()
			return nil
		}); err != nil {
			return nil, nil, err
		}

		// Do not return the new QID.
		//
		// TODO: why?
		return nil, newRef, nil
	}

	// Do the walk, one element at a time.
	walkRef := ref
	walkRef.IncRef()
	for i := 0; i < len(names); i++ {
		// We won't allow beyond past symlinks; stop here if this isn't
		// a proper directory and we have additional paths to walk.
		if !walkRef.isDir {
			walkRef.DecRef() // Drop walk reference; no lock required.
			return nil, nil, linux.EINVAL
		}

		if err := walkRef.safelyRead(func() (err error) {
			// Pass getattr = true to walkOne since we need the file type for
			// newRef.
			localQIDs, newRef, err := walkOne(walkRef, names[i:i+1])
			if err != nil {
				return err
			}
			qids = append(qids, localQIDs...)

			walkRef.pathNode.addChild(newRef, names[i])
			// We allow our walk reference to become the new parent
			// reference here and so we don't IncRef. Instead, just
			// set walkRef to the newRef above and acquire a new
			// walk reference.
			walkRef = newRef
			walkRef.IncRef()
			return nil
		}); err != nil {
			walkRef.DecRef() // Drop the old walkRef.
			return nil, nil, err
		}
	}

	// Success.
	return qids, walkRef, nil
}

// handle implements handler.handle for the 9P2000 Twalk.
func (t *twalk) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// Do the walk.
	qids, newRef, err := doWalk(cs, ref, t.Names, walkOneLegacy)
	if err != nil {
		return cs.session.newErr(err)
	}
	defer newRef.DecRef()

	// Install the new fid.
	cs.session.InsertFID(t.newFID, newRef)
	return &rwalk{QIDs: qids}
}

// handle implements handler.handle for the 9P2000.L Twalk.
func (t *tlwalk) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	// Do the walk.
	qids, newRef, err := doWalk(cs, ref, t.Names, func(from *fidRef, names []string) ([]QID, *fidRef, error) {
		qids, newRef, _, _, err := walkOneLinux(from, names, false)
		return qids, newRef, err
	})
	if err != nil {
		return cs.session.newErr(err)
	}
	defer newRef.DecRef()

	// Install the new fid.
	cs.session.InsertFID(t.newFID, newRef)
	return &rwalk{QIDs: qids}
}

// handle implements handler.handle.
func (t *twalkgetattr) handle(cs *connState) message {
	// Lookup the fid.
	ref, ok := cs.session.LookupFID(t.fid)
	if !ok {
		return cs.session.newErr(linux.EBADF)
	}
	defer ref.DecRef()

	var valid AttrMask
	var attr Attr
	// Do the walk.
	qids, newRef, err := doWalk(cs, ref, t.Names, func(from *fidRef, names []string) (qids []QID, newRef *fidRef, err error) {
		qids, newRef, valid, attr, err = walkOneLinux(from, names, true)
		return
	})
	if err != nil {
		return cs.session.newErr(err)
	}
	defer newRef.DecRef()

	// Install the new fid.
	cs.session.InsertFID(t.newFID, newRef)
	return &rwalkgetattr{QIDs: qids, Valid: valid, Attr: attr}
}

// handle implements handler.handle.
func (t *tucreate) handle(cs *connState) message {
	rlcreate, err := t.tlcreate.do(cs, t.UID)
	if err != nil {
		return cs.session.newErr(err)
	}
	return &rucreate{*rlcreate}
}

// handle implements handler.handle.
func (t *tumkdir) handle(cs *connState) message {
	rmkdir, err := t.tmkdir.do(cs, t.UID)
	if err != nil {
		return cs.session.newErr(err)
	}
	return &rumkdir{*rmkdir}
}

// handle implements handler.handle.
func (t *tusymlink) handle(cs *connState) message {
	rsymlink, err := t.tsymlink.do(cs, t.UID)
	if err != nil {
		return cs.session.newErr(err)
	}
	return &rusymlink{*rsymlink}
}

// handle implements handler.handle.
func (t *tumknod) handle(cs *connState) message {
	rmknod, err := t.tmknod.do(cs, t.UID)
	if err != nil {
		return cs.session.newErr(err)
	}
	return &rumknod{*rmknod}
}
