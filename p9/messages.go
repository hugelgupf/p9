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
	"math"
)

// ErrInvalidMsgType is returned when an unsupported message type is found.
type ErrInvalidMsgType struct {
	msgType
}

// Error returns a useful string.
func (e *ErrInvalidMsgType) Error() string {
	return fmt.Sprintf("invalid message type: %d", e.msgType)
}

// message is a generic 9P message.
type message interface {
	encoder
	fmt.Stringer

	// Type returns the message type number.
	Type() msgType
}

// payloader is a special message which may include an inline payload.
type payloader interface {
	// FixedSize returns the size of the fixed portion of this message.
	FixedSize() uint32

	// Payload returns the payload for sending.
	Payload() []byte

	// SetPayload returns the decoded message.
	//
	// This is going to be total message size - FixedSize. But this should
	// be validated during Decode, which will be called after SetPayload.
	SetPayload([]byte)
}

// tversion is a version request.
type tversion struct {
	// MSize is the message size to use.
	MSize uint32

	// Version is the version string.
	//
	// For this implementation, this must be 9P2000.L.
	Version string
}

// Decode implements encoder.Decode.
func (t *tversion) Decode(b *buffer) {
	t.MSize = b.Read32()
	t.Version = b.ReadString()
}

// Encode implements encoder.Encode.
func (t *tversion) Encode(b *buffer) {
	b.Write32(t.MSize)
	b.WriteString(t.Version)
}

// Type implements message.Type.
func (*tversion) Type() msgType {
	return msgTversion
}

// String implements fmt.Stringer.
func (t *tversion) String() string {
	return fmt.Sprintf("Tversion{MSize: %d, Version: %s}", t.MSize, t.Version)
}

// rversion is a version response.
type rversion struct {
	// MSize is the negotiated size.
	MSize uint32

	// Version is the negotiated version.
	Version string
}

// Decode implements encoder.Decode.
func (r *rversion) Decode(b *buffer) {
	r.MSize = b.Read32()
	r.Version = b.ReadString()
}

// Encode implements encoder.Encode.
func (r *rversion) Encode(b *buffer) {
	b.Write32(r.MSize)
	b.WriteString(r.Version)
}

// Type implements message.Type.
func (*rversion) Type() msgType {
	return msgRversion
}

// String implements fmt.Stringer.
func (r *rversion) String() string {
	return fmt.Sprintf("Rversion{MSize: %d, Version: %s}", r.MSize, r.Version)
}

// tflush is a flush request.
type tflush struct {
	// OldTag is the tag to wait on.
	OldTag Tag
}

// Decode implements encoder.Decode.
func (t *tflush) Decode(b *buffer) {
	t.OldTag = b.ReadTag()
}

// Encode implements encoder.Encode.
func (t *tflush) Encode(b *buffer) {
	b.WriteTag(t.OldTag)
}

// Type implements message.Type.
func (*tflush) Type() msgType {
	return msgTflush
}

// String implements fmt.Stringer.
func (t *tflush) String() string {
	return fmt.Sprintf("Tflush{OldTag: %d}", t.OldTag)
}

// rflush is a flush response.
type rflush struct {
}

// Decode implements encoder.Decode.
func (*rflush) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rflush) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rflush) Type() msgType {
	return msgRflush
}

// String implements fmt.Stringer.
func (r *rflush) String() string {
	return fmt.Sprintf("Rflush{}")
}

// twalk is a walk request.
type twalk struct {
	// FID is the FID to be walked.
	FID FID

	// NewFID is the resulting FID.
	NewFID FID

	// Names are the set of names to be walked.
	Names []string
}

// Decode implements encoder.Decode.
func (t *twalk) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.NewFID = b.ReadFID()
	n := b.Read16()
	t.Names = t.Names[:0]
	for i := 0; i < int(n); i++ {
		t.Names = append(t.Names, b.ReadString())
	}
}

// Encode implements encoder.Encode.
func (t *twalk) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.WriteFID(t.NewFID)
	b.Write16(uint16(len(t.Names)))
	for _, name := range t.Names {
		b.WriteString(name)
	}
}

// Type implements message.Type.
func (*twalk) Type() msgType {
	return msgTwalk
}

// String implements fmt.Stringer.
func (t *twalk) String() string {
	return fmt.Sprintf("Twalk{FID: %d, NewFID: %d, Names: %v}", t.FID, t.NewFID, t.Names)
}

// rwalk is a walk response.
type rwalk struct {
	// QIDs are the set of QIDs returned.
	QIDs []QID
}

// Decode implements encoder.Decode.
func (r *rwalk) Decode(b *buffer) {
	n := b.Read16()
	r.QIDs = r.QIDs[:0]
	for i := 0; i < int(n); i++ {
		var q QID
		q.Decode(b)
		r.QIDs = append(r.QIDs, q)
	}
}

// Encode implements encoder.Encode.
func (r *rwalk) Encode(b *buffer) {
	b.Write16(uint16(len(r.QIDs)))
	for _, q := range r.QIDs {
		q.Encode(b)
	}
}

// Type implements message.Type.
func (*rwalk) Type() msgType {
	return msgRwalk
}

// String implements fmt.Stringer.
func (r *rwalk) String() string {
	return fmt.Sprintf("Rwalk{QIDs: %v}", r.QIDs)
}

// tclunk is a close request.
type tclunk struct {
	// FID is the FID to be closed.
	FID FID
}

// Decode implements encoder.Decode.
func (t *tclunk) Decode(b *buffer) {
	t.FID = b.ReadFID()
}

// Encode implements encoder.Encode.
func (t *tclunk) Encode(b *buffer) {
	b.WriteFID(t.FID)
}

// Type implements message.Type.
func (*tclunk) Type() msgType {
	return msgTclunk
}

// String implements fmt.Stringer.
func (t *tclunk) String() string {
	return fmt.Sprintf("Tclunk{FID: %d}", t.FID)
}

// rclunk is a close response.
type rclunk struct{}

// Decode implements encoder.Decode.
func (*rclunk) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rclunk) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rclunk) Type() msgType {
	return msgRclunk
}

// String implements fmt.Stringer.
func (r *rclunk) String() string {
	return fmt.Sprintf("Rclunk{}")
}

// tremove is a remove request.
type tremove struct {
	// FID is the FID to be removed.
	FID FID
}

// Decode implements encoder.Decode.
func (t *tremove) Decode(b *buffer) {
	t.FID = b.ReadFID()
}

// Encode implements encoder.Encode.
func (t *tremove) Encode(b *buffer) {
	b.WriteFID(t.FID)
}

// Type implements message.Type.
func (*tremove) Type() msgType {
	return msgTremove
}

// String implements fmt.Stringer.
func (t *tremove) String() string {
	return fmt.Sprintf("Tremove{FID: %d}", t.FID)
}

// rremove is a remove response.
type rremove struct {
}

// Decode implements encoder.Decode.
func (*rremove) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rremove) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rremove) Type() msgType {
	return msgRremove
}

// String implements fmt.Stringer.
func (r *rremove) String() string {
	return fmt.Sprintf("Rremove{}")
}

// rlerror is an error response.
//
// Note that this replaces the error code used in 9p.
type rlerror struct {
	Error uint32
}

// Decode implements encoder.Decode.
func (r *rlerror) Decode(b *buffer) {
	r.Error = b.Read32()
}

// Encode implements encoder.Encode.
func (r *rlerror) Encode(b *buffer) {
	b.Write32(r.Error)
}

// Type implements message.Type.
func (*rlerror) Type() msgType {
	return msgRlerror
}

// String implements fmt.Stringer.
func (r *rlerror) String() string {
	return fmt.Sprintf("Rlerror{Error: %d}", r.Error)
}

// tauth is an authentication request.
type tauth struct {
	// AuthenticationFID is the FID to attach the authentication result.
	AuthenticationFID FID

	// UserName is the user to attach.
	UserName string

	// AttachName is the attach name.
	AttachName string

	// UserID is the numeric identifier for UserName.
	UID UID
}

// Decode implements encoder.Decode.
func (t *tauth) Decode(b *buffer) {
	t.AuthenticationFID = b.ReadFID()
	t.UserName = b.ReadString()
	t.AttachName = b.ReadString()
	t.UID = b.ReadUID()
}

// Encode implements encoder.Encode.
func (t *tauth) Encode(b *buffer) {
	b.WriteFID(t.AuthenticationFID)
	b.WriteString(t.UserName)
	b.WriteString(t.AttachName)
	b.WriteUID(t.UID)
}

// Type implements message.Type.
func (*tauth) Type() msgType {
	return msgTauth
}

// String implements fmt.Stringer.
func (t *tauth) String() string {
	return fmt.Sprintf("Tauth{AuthFID: %d, UserName: %s, AttachName: %s, UID: %d", t.AuthenticationFID, t.UserName, t.AttachName, t.UID)
}

// rauth is an authentication response.
//
// Encode, Decode and Length are inherited directly from QID.
type rauth struct {
	QID
}

// Type implements message.Type.
func (*rauth) Type() msgType {
	return msgRauth
}

// String implements fmt.Stringer.
func (r *rauth) String() string {
	return fmt.Sprintf("Rauth{QID: %s}", r.QID)
}

// tattach is an attach request.
type tattach struct {
	// FID is the FID to be attached.
	FID FID

	// Auth is the embedded authentication request.
	//
	// See client.Attach for information regarding authentication.
	Auth tauth
}

// Decode implements encoder.Decode.
func (t *tattach) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Auth.Decode(b)
}

// Encode implements encoder.Encode.
func (t *tattach) Encode(b *buffer) {
	b.WriteFID(t.FID)
	t.Auth.Encode(b)
}

// Type implements message.Type.
func (*tattach) Type() msgType {
	return msgTattach
}

// String implements fmt.Stringer.
func (t *tattach) String() string {
	return fmt.Sprintf("Tattach{FID: %d, AuthFID: %d, UserName: %s, AttachName: %s, UID: %d}", t.FID, t.Auth.AuthenticationFID, t.Auth.UserName, t.Auth.AttachName, t.Auth.UID)
}

// rattach is an attach response.
type rattach struct {
	QID
}

// Type implements message.Type.
func (*rattach) Type() msgType {
	return msgRattach
}

// String implements fmt.Stringer.
func (r *rattach) String() string {
	return fmt.Sprintf("Rattach{QID: %s}", r.QID)
}

// tlopen is an open request.
type tlopen struct {
	// FID is the FID to be opened.
	FID FID

	// Flags are the open flags.
	Flags OpenFlags
}

// Decode implements encoder.Decode.
func (t *tlopen) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Flags = b.ReadOpenFlags()
}

// Encode implements encoder.Encode.
func (t *tlopen) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.WriteOpenFlags(t.Flags)
}

// Type implements message.Type.
func (*tlopen) Type() msgType {
	return msgTlopen
}

// String implements fmt.Stringer.
func (t *tlopen) String() string {
	return fmt.Sprintf("Tlopen{FID: %d, Flags: %v}", t.FID, t.Flags)
}

// rlopen is a open response.
type rlopen struct {
	// QID is the file's QID.
	QID QID

	// IoUnit is the recommended I/O unit.
	IoUnit uint32
}

// Decode implements encoder.Decode.
func (r *rlopen) Decode(b *buffer) {
	r.QID.Decode(b)
	r.IoUnit = b.Read32()
}

// Encode implements encoder.Encode.
func (r *rlopen) Encode(b *buffer) {
	r.QID.Encode(b)
	b.Write32(r.IoUnit)
}

// Type implements message.Type.
func (*rlopen) Type() msgType {
	return msgRlopen
}

// String implements fmt.Stringer.
func (r *rlopen) String() string {
	return fmt.Sprintf("Rlopen{QID: %s, IoUnit: %d}", r.QID, r.IoUnit)
}

// tlcreate is a create request.
type tlcreate struct {
	// FID is the parent FID.
	//
	// This becomes the new file.
	FID FID

	// Name is the file name to create.
	Name string

	// Mode is the open mode (O_RDWR, etc.).
	//
	// Note that flags like O_TRUNC are ignored, as is O_EXCL. All
	// create operations are exclusive.
	OpenFlags OpenFlags

	// Permissions is the set of permission bits.
	Permissions FileMode

	// GID is the group ID to use for creating the file.
	GID GID
}

// Decode implements encoder.Decode.
func (t *tlcreate) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Name = b.ReadString()
	t.OpenFlags = b.ReadOpenFlags()
	t.Permissions = b.ReadPermissions()
	t.GID = b.ReadGID()
}

// Encode implements encoder.Encode.
func (t *tlcreate) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.WriteString(t.Name)
	b.WriteOpenFlags(t.OpenFlags)
	b.WritePermissions(t.Permissions)
	b.WriteGID(t.GID)
}

// Type implements message.Type.
func (*tlcreate) Type() msgType {
	return msgTlcreate
}

// String implements fmt.Stringer.
func (t *tlcreate) String() string {
	return fmt.Sprintf("Tlcreate{FID: %d, Name: %s, OpenFlags: %s, Permissions: 0o%o, GID: %d}", t.FID, t.Name, t.OpenFlags, t.Permissions, t.GID)
}

// rlcreate is a create response.
//
// The Encode, Decode, etc. methods are inherited from Rlopen.
type rlcreate struct {
	rlopen
}

// Type implements message.Type.
func (*rlcreate) Type() msgType {
	return msgRlcreate
}

// String implements fmt.Stringer.
func (r *rlcreate) String() string {
	return fmt.Sprintf("Rlcreate{QID: %s, IoUnit: %d}", r.QID, r.IoUnit)
}

// tsymlink is a symlink request.
type tsymlink struct {
	// Directory is the directory FID.
	Directory FID

	// Name is the new in the directory.
	Name string

	// Target is the symlink target.
	Target string

	// GID is the owning group.
	GID GID
}

// Decode implements encoder.Decode.
func (t *tsymlink) Decode(b *buffer) {
	t.Directory = b.ReadFID()
	t.Name = b.ReadString()
	t.Target = b.ReadString()
	t.GID = b.ReadGID()
}

// Encode implements encoder.Encode.
func (t *tsymlink) Encode(b *buffer) {
	b.WriteFID(t.Directory)
	b.WriteString(t.Name)
	b.WriteString(t.Target)
	b.WriteGID(t.GID)
}

// Type implements message.Type.
func (*tsymlink) Type() msgType {
	return msgTsymlink
}

// String implements fmt.Stringer.
func (t *tsymlink) String() string {
	return fmt.Sprintf("Tsymlink{DirectoryFID: %d, Name: %s, Target: %s, GID: %d}", t.Directory, t.Name, t.Target, t.GID)
}

// rsymlink is a symlink response.
type rsymlink struct {
	// QID is the new symlink's QID.
	QID QID
}

// Decode implements encoder.Decode.
func (r *rsymlink) Decode(b *buffer) {
	r.QID.Decode(b)
}

// Encode implements encoder.Encode.
func (r *rsymlink) Encode(b *buffer) {
	r.QID.Encode(b)
}

// Type implements message.Type.
func (*rsymlink) Type() msgType {
	return msgRsymlink
}

// String implements fmt.Stringer.
func (r *rsymlink) String() string {
	return fmt.Sprintf("Rsymlink{QID: %s}", r.QID)
}

// tlink is a link request.
type tlink struct {
	// Directory is the directory to contain the link.
	Directory FID

	// FID is the target.
	Target FID

	// Name is the new source name.
	Name string
}

// Decode implements encoder.Decode.
func (t *tlink) Decode(b *buffer) {
	t.Directory = b.ReadFID()
	t.Target = b.ReadFID()
	t.Name = b.ReadString()
}

// Encode implements encoder.Encode.
func (t *tlink) Encode(b *buffer) {
	b.WriteFID(t.Directory)
	b.WriteFID(t.Target)
	b.WriteString(t.Name)
}

// Type implements message.Type.
func (*tlink) Type() msgType {
	return msgTlink
}

// String implements fmt.Stringer.
func (t *tlink) String() string {
	return fmt.Sprintf("Tlink{DirectoryFID: %d, TargetFID: %d, Name: %s}", t.Directory, t.Target, t.Name)
}

// rlink is a link response.
type rlink struct {
}

// Type implements message.Type.
func (*rlink) Type() msgType {
	return msgRlink
}

// Decode implements encoder.Decode.
func (*rlink) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rlink) Encode(b *buffer) {
}

// String implements fmt.Stringer.
func (r *rlink) String() string {
	return fmt.Sprintf("Rlink{}")
}

// trenameat is a rename request.
type trenameat struct {
	// OldDirectory is the source directory.
	OldDirectory FID

	// OldName is the source file name.
	OldName string

	// NewDirectory is the target directory.
	NewDirectory FID

	// NewName is the new file name.
	NewName string
}

// Decode implements encoder.Decode.
func (t *trenameat) Decode(b *buffer) {
	t.OldDirectory = b.ReadFID()
	t.OldName = b.ReadString()
	t.NewDirectory = b.ReadFID()
	t.NewName = b.ReadString()
}

// Encode implements encoder.Encode.
func (t *trenameat) Encode(b *buffer) {
	b.WriteFID(t.OldDirectory)
	b.WriteString(t.OldName)
	b.WriteFID(t.NewDirectory)
	b.WriteString(t.NewName)
}

// Type implements message.Type.
func (*trenameat) Type() msgType {
	return msgTrenameat
}

// String implements fmt.Stringer.
func (t *trenameat) String() string {
	return fmt.Sprintf("TrenameAt{OldDirectoryFID: %d, OldName: %s, NewDirectoryFID: %d, NewName: %s}", t.OldDirectory, t.OldName, t.NewDirectory, t.NewName)
}

// rrenameat is a rename response.
type rrenameat struct {
}

// Decode implements encoder.Decode.
func (*rrenameat) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rrenameat) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rrenameat) Type() msgType {
	return msgRrenameat
}

// String implements fmt.Stringer.
func (r *rrenameat) String() string {
	return fmt.Sprintf("Rrenameat{}")
}

// tunlinkat is an unlink request.
type tunlinkat struct {
	// Directory is the originating directory.
	Directory FID

	// Name is the name of the entry to unlink.
	Name string

	// Flags are extra flags (e.g. O_DIRECTORY). These are not interpreted by p9.
	Flags uint32
}

// Decode implements encoder.Decode.
func (t *tunlinkat) Decode(b *buffer) {
	t.Directory = b.ReadFID()
	t.Name = b.ReadString()
	t.Flags = b.Read32()
}

// Encode implements encoder.Encode.
func (t *tunlinkat) Encode(b *buffer) {
	b.WriteFID(t.Directory)
	b.WriteString(t.Name)
	b.Write32(t.Flags)
}

// Type implements message.Type.
func (*tunlinkat) Type() msgType {
	return msgTunlinkat
}

// String implements fmt.Stringer.
func (t *tunlinkat) String() string {
	return fmt.Sprintf("Tunlinkat{DirectoryFID: %d, Name: %s, Flags: 0x%X}", t.Directory, t.Name, t.Flags)
}

// runlinkat is an unlink response.
type runlinkat struct {
}

// Decode implements encoder.Decode.
func (*runlinkat) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*runlinkat) Encode(b *buffer) {
}

// Type implements message.Type.
func (*runlinkat) Type() msgType {
	return msgRunlinkat
}

// String implements fmt.Stringer.
func (r *runlinkat) String() string {
	return fmt.Sprintf("Runlinkat{}")
}

// trename is a rename request.
type trename struct {
	// FID is the FID to rename.
	FID FID

	// Directory is the target directory.
	Directory FID

	// Name is the new file name.
	Name string
}

// Decode implements encoder.Decode.
func (t *trename) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Directory = b.ReadFID()
	t.Name = b.ReadString()
}

// Encode implements encoder.Encode.
func (t *trename) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.WriteFID(t.Directory)
	b.WriteString(t.Name)
}

// Type implements message.Type.
func (*trename) Type() msgType {
	return msgTrename
}

// String implements fmt.Stringer.
func (t *trename) String() string {
	return fmt.Sprintf("Trename{FID: %d, DirectoryFID: %d, Name: %s}", t.FID, t.Directory, t.Name)
}

// rrename is a rename response.
type rrename struct {
}

// Decode implements encoder.Decode.
func (*rrename) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rrename) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rrename) Type() msgType {
	return msgRrename
}

// String implements fmt.Stringer.
func (r *rrename) String() string {
	return fmt.Sprintf("Rrename{}")
}

// treadlink is a readlink request.
type treadlink struct {
	// FID is the symlink.
	FID FID
}

// Decode implements encoder.Decode.
func (t *treadlink) Decode(b *buffer) {
	t.FID = b.ReadFID()
}

// Encode implements encoder.Encode.
func (t *treadlink) Encode(b *buffer) {
	b.WriteFID(t.FID)
}

// Type implements message.Type.
func (*treadlink) Type() msgType {
	return msgTreadlink
}

// String implements fmt.Stringer.
func (t *treadlink) String() string {
	return fmt.Sprintf("Treadlink{FID: %d}", t.FID)
}

// rreadlink is a readlink response.
type rreadlink struct {
	// Target is the symlink target.
	Target string
}

// Decode implements encoder.Decode.
func (r *rreadlink) Decode(b *buffer) {
	r.Target = b.ReadString()
}

// Encode implements encoder.Encode.
func (r *rreadlink) Encode(b *buffer) {
	b.WriteString(r.Target)
}

// Type implements message.Type.
func (*rreadlink) Type() msgType {
	return msgRreadlink
}

// String implements fmt.Stringer.
func (r *rreadlink) String() string {
	return fmt.Sprintf("Rreadlink{Target: %s}", r.Target)
}

// tread is a read request.
type tread struct {
	// FID is the FID to read.
	FID FID

	// Offset indicates the file offset.
	Offset uint64

	// Count indicates the number of bytes to read.
	Count uint32
}

// Decode implements encoder.Decode.
func (t *tread) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Offset = b.Read64()
	t.Count = b.Read32()
}

// Encode implements encoder.Encode.
func (t *tread) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.Write64(t.Offset)
	b.Write32(t.Count)
}

// Type implements message.Type.
func (*tread) Type() msgType {
	return msgTread
}

// String implements fmt.Stringer.
func (t *tread) String() string {
	return fmt.Sprintf("Tread{FID: %d, Offset: %d, Count: %d}", t.FID, t.Offset, t.Count)
}

// rread is the response for a Tread.
type rread struct {
	// Data is the resulting data.
	Data []byte
}

// Decode implements encoder.Decode.
//
// Data is automatically decoded via Payload.
func (r *rread) Decode(b *buffer) {
	count := b.Read32()
	if count != uint32(len(r.Data)) {
		b.markOverrun()
	}
}

// Encode implements encoder.Encode.
//
// Data is automatically encoded via Payload.
func (r *rread) Encode(b *buffer) {
	b.Write32(uint32(len(r.Data)))
}

// Type implements message.Type.
func (*rread) Type() msgType {
	return msgRread
}

// FixedSize implements payloader.FixedSize.
func (*rread) FixedSize() uint32 {
	return 4
}

// Payload implements payloader.Payload.
func (r *rread) Payload() []byte {
	return r.Data
}

// SetPayload implements payloader.SetPayload.
func (r *rread) SetPayload(p []byte) {
	r.Data = p
}

// String implements fmt.Stringer.
func (r *rread) String() string {
	return fmt.Sprintf("Rread{len(Data): %d}", len(r.Data))
}

// twrite is a write request.
type twrite struct {
	// FID is the FID to read.
	FID FID

	// Offset indicates the file offset.
	Offset uint64

	// Data is the data to be written.
	Data []byte
}

// Decode implements encoder.Decode.
func (t *twrite) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Offset = b.Read64()
	count := b.Read32()
	if count != uint32(len(t.Data)) {
		b.markOverrun()
	}
}

// Encode implements encoder.Encode.
//
// This uses the buffer payload to avoid a copy.
func (t *twrite) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.Write64(t.Offset)
	b.Write32(uint32(len(t.Data)))
}

// Type implements message.Type.
func (*twrite) Type() msgType {
	return msgTwrite
}

// FixedSize implements payloader.FixedSize.
func (*twrite) FixedSize() uint32 {
	return 16
}

// Payload implements payloader.Payload.
func (t *twrite) Payload() []byte {
	return t.Data
}

// SetPayload implements payloader.SetPayload.
func (t *twrite) SetPayload(p []byte) {
	t.Data = p
}

// String implements fmt.Stringer.
func (t *twrite) String() string {
	return fmt.Sprintf("Twrite{FID: %v, Offset %d, len(Data): %d}", t.FID, t.Offset, len(t.Data))
}

// rwrite is the response for a Twrite.
type rwrite struct {
	// Count indicates the number of bytes successfully written.
	Count uint32
}

// Decode implements encoder.Decode.
func (r *rwrite) Decode(b *buffer) {
	r.Count = b.Read32()
}

// Encode implements encoder.Encode.
func (r *rwrite) Encode(b *buffer) {
	b.Write32(r.Count)
}

// Type implements message.Type.
func (*rwrite) Type() msgType {
	return msgRwrite
}

// String implements fmt.Stringer.
func (r *rwrite) String() string {
	return fmt.Sprintf("Rwrite{Count: %d}", r.Count)
}

// tmknod is a mknod request.
type tmknod struct {
	// Directory is the parent directory.
	Directory FID

	// Name is the device name.
	Name string

	// Mode is the device mode and permissions.
	Mode FileMode

	// Major is the device major number.
	Major uint32

	// Minor is the device minor number.
	Minor uint32

	// GID is the device GID.
	GID GID
}

// Decode implements encoder.Decode.
func (t *tmknod) Decode(b *buffer) {
	t.Directory = b.ReadFID()
	t.Name = b.ReadString()
	t.Mode = b.ReadFileMode()
	t.Major = b.Read32()
	t.Minor = b.Read32()
	t.GID = b.ReadGID()
}

// Encode implements encoder.Encode.
func (t *tmknod) Encode(b *buffer) {
	b.WriteFID(t.Directory)
	b.WriteString(t.Name)
	b.WriteFileMode(t.Mode)
	b.Write32(t.Major)
	b.Write32(t.Minor)
	b.WriteGID(t.GID)
}

// Type implements message.Type.
func (*tmknod) Type() msgType {
	return msgTmknod
}

// String implements fmt.Stringer.
func (t *tmknod) String() string {
	return fmt.Sprintf("Tmknod{DirectoryFID: %d, Name: %s, Mode: 0o%o, Major: %d, Minor: %d, GID: %d}", t.Directory, t.Name, t.Mode, t.Major, t.Minor, t.GID)
}

// rmknod is a mknod response.
type rmknod struct {
	// QID is the resulting QID.
	QID QID
}

// Decode implements encoder.Decode.
func (r *rmknod) Decode(b *buffer) {
	r.QID.Decode(b)
}

// Encode implements encoder.Encode.
func (r *rmknod) Encode(b *buffer) {
	r.QID.Encode(b)
}

// Type implements message.Type.
func (*rmknod) Type() msgType {
	return msgRmknod
}

// String implements fmt.Stringer.
func (r *rmknod) String() string {
	return fmt.Sprintf("Rmknod{QID: %s}", r.QID)
}

// tmkdir is a mkdir request.
type tmkdir struct {
	// Directory is the parent directory.
	Directory FID

	// Name is the new directory name.
	Name string

	// Permissions is the set of permission bits.
	Permissions FileMode

	// GID is the owning group.
	GID GID
}

// Decode implements encoder.Decode.
func (t *tmkdir) Decode(b *buffer) {
	t.Directory = b.ReadFID()
	t.Name = b.ReadString()
	t.Permissions = b.ReadPermissions()
	t.GID = b.ReadGID()
}

// Encode implements encoder.Encode.
func (t *tmkdir) Encode(b *buffer) {
	b.WriteFID(t.Directory)
	b.WriteString(t.Name)
	b.WritePermissions(t.Permissions)
	b.WriteGID(t.GID)
}

// Type implements message.Type.
func (*tmkdir) Type() msgType {
	return msgTmkdir
}

// String implements fmt.Stringer.
func (t *tmkdir) String() string {
	return fmt.Sprintf("Tmkdir{DirectoryFID: %d, Name: %s, Permissions: 0o%o, GID: %d}", t.Directory, t.Name, t.Permissions, t.GID)
}

// rmkdir is a mkdir response.
type rmkdir struct {
	// QID is the resulting QID.
	QID QID
}

// Decode implements encoder.Decode.
func (r *rmkdir) Decode(b *buffer) {
	r.QID.Decode(b)
}

// Encode implements encoder.Encode.
func (r *rmkdir) Encode(b *buffer) {
	r.QID.Encode(b)
}

// Type implements message.Type.
func (*rmkdir) Type() msgType {
	return msgRmkdir
}

// String implements fmt.Stringer.
func (r *rmkdir) String() string {
	return fmt.Sprintf("Rmkdir{QID: %s}", r.QID)
}

// tgetattr is a getattr request.
type tgetattr struct {
	// FID is the FID to get attributes for.
	FID FID

	// AttrMask is the set of attributes to get.
	AttrMask AttrMask
}

// Decode implements encoder.Decode.
func (t *tgetattr) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.AttrMask.Decode(b)
}

// Encode implements encoder.Encode.
func (t *tgetattr) Encode(b *buffer) {
	b.WriteFID(t.FID)
	t.AttrMask.Encode(b)
}

// Type implements message.Type.
func (*tgetattr) Type() msgType {
	return msgTgetattr
}

// String implements fmt.Stringer.
func (t *tgetattr) String() string {
	return fmt.Sprintf("Tgetattr{FID: %d, AttrMask: %s}", t.FID, t.AttrMask)
}

// rgetattr is a getattr response.
type rgetattr struct {
	// Valid indicates which fields are valid.
	Valid AttrMask

	// QID is the QID for this file.
	QID

	// Attr is the set of attributes.
	Attr Attr
}

// Decode implements encoder.Decode.
func (r *rgetattr) Decode(b *buffer) {
	r.Valid.Decode(b)
	r.QID.Decode(b)
	r.Attr.Decode(b)
}

// Encode implements encoder.Encode.
func (r *rgetattr) Encode(b *buffer) {
	r.Valid.Encode(b)
	r.QID.Encode(b)
	r.Attr.Encode(b)
}

// Type implements message.Type.
func (*rgetattr) Type() msgType {
	return msgRgetattr
}

// String implements fmt.Stringer.
func (r *rgetattr) String() string {
	return fmt.Sprintf("Rgetattr{Valid: %v, QID: %s, Attr: %s}", r.Valid, r.QID, r.Attr)
}

// tsetattr is a setattr request.
type tsetattr struct {
	// FID is the FID to change.
	FID FID

	// Valid is the set of bits which will be used.
	Valid SetAttrMask

	// SetAttr is the set request.
	SetAttr SetAttr
}

// Decode implements encoder.Decode.
func (t *tsetattr) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Valid.Decode(b)
	t.SetAttr.Decode(b)
}

// Encode implements encoder.Encode.
func (t *tsetattr) Encode(b *buffer) {
	b.WriteFID(t.FID)
	t.Valid.Encode(b)
	t.SetAttr.Encode(b)
}

// Type implements message.Type.
func (*tsetattr) Type() msgType {
	return msgTsetattr
}

// String implements fmt.Stringer.
func (t *tsetattr) String() string {
	return fmt.Sprintf("Tsetattr{FID: %d, Valid: %v, SetAttr: %s}", t.FID, t.Valid, t.SetAttr)
}

// rsetattr is a setattr response.
type rsetattr struct {
}

// Decode implements encoder.Decode.
func (*rsetattr) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rsetattr) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rsetattr) Type() msgType {
	return msgRsetattr
}

// String implements fmt.Stringer.
func (r *rsetattr) String() string {
	return fmt.Sprintf("Rsetattr{}")
}

// tallocate is an allocate request. This is an extension to 9P protocol, not
// present in the 9P2000.L standard.
type tallocate struct {
	FID    FID
	Mode   AllocateMode
	Offset uint64
	Length uint64
}

// Decode implements encoder.Decode.
func (t *tallocate) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Mode.Decode(b)
	t.Offset = b.Read64()
	t.Length = b.Read64()
}

// Encode implements encoder.Encode.
func (t *tallocate) Encode(b *buffer) {
	b.WriteFID(t.FID)
	t.Mode.Encode(b)
	b.Write64(t.Offset)
	b.Write64(t.Length)
}

// Type implements message.Type.
func (*tallocate) Type() msgType {
	return msgTallocate
}

// String implements fmt.Stringer.
func (t *tallocate) String() string {
	return fmt.Sprintf("Tallocate{FID: %d, Offset: %d, Length: %d}", t.FID, t.Offset, t.Length)
}

// rallocate is an allocate response.
type rallocate struct {
}

// Decode implements encoder.Decode.
func (*rallocate) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rallocate) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rallocate) Type() msgType {
	return msgRallocate
}

// String implements fmt.Stringer.
func (r *rallocate) String() string {
	return fmt.Sprintf("Rallocate{}")
}

// txattrwalk walks extended attributes.
type txattrwalk struct {
	// FID is the FID to check for attributes.
	FID FID

	// NewFID is the new FID associated with the attributes.
	NewFID FID

	// Name is the attribute name.
	Name string
}

// Decode implements encoder.Decode.
func (t *txattrwalk) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.NewFID = b.ReadFID()
	t.Name = b.ReadString()
}

// Encode implements encoder.Encode.
func (t *txattrwalk) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.WriteFID(t.NewFID)
	b.WriteString(t.Name)
}

// Type implements message.Type.
func (*txattrwalk) Type() msgType {
	return msgTxattrwalk
}

// String implements fmt.Stringer.
func (t *txattrwalk) String() string {
	return fmt.Sprintf("Txattrwalk{FID: %d, NewFID: %d, Name: %s}", t.FID, t.NewFID, t.Name)
}

// rxattrwalk is a xattrwalk response.
type rxattrwalk struct {
	// Size is the size of the extended attribute.
	Size uint64
}

// Decode implements encoder.Decode.
func (r *rxattrwalk) Decode(b *buffer) {
	r.Size = b.Read64()
}

// Encode implements encoder.Encode.
func (r *rxattrwalk) Encode(b *buffer) {
	b.Write64(r.Size)
}

// Type implements message.Type.
func (*rxattrwalk) Type() msgType {
	return msgRxattrwalk
}

// String implements fmt.Stringer.
func (r *rxattrwalk) String() string {
	return fmt.Sprintf("Rxattrwalk{Size: %d}", r.Size)
}

// txattrcreate prepare to set extended attributes.
type txattrcreate struct {
	// FID is input/output parameter, it identifies the file on which
	// extended attributes will be set but after successful Rxattrcreate
	// it is used to write the extended attribute value.
	FID FID

	// Name is the attribute name.
	Name string

	// Size of the attribute value. When the FID is clunked it has to match
	// the number of bytes written to the FID.
	AttrSize uint64

	// Linux setxattr(2) flags.
	Flags uint32
}

// Decode implements encoder.Decode.
func (t *txattrcreate) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.Name = b.ReadString()
	t.AttrSize = b.Read64()
	t.Flags = b.Read32()
}

// Encode implements encoder.Encode.
func (t *txattrcreate) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.WriteString(t.Name)
	b.Write64(t.AttrSize)
	b.Write32(t.Flags)
}

// Type implements message.Type.
func (*txattrcreate) Type() msgType {
	return msgTxattrcreate
}

// String implements fmt.Stringer.
func (t *txattrcreate) String() string {
	return fmt.Sprintf("Txattrcreate{FID: %d, Name: %s, AttrSize: %d, Flags: %d}", t.FID, t.Name, t.AttrSize, t.Flags)
}

// rxattrcreate is a xattrcreate response.
type rxattrcreate struct {
}

// Decode implements encoder.Decode.
func (r *rxattrcreate) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (r *rxattrcreate) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rxattrcreate) Type() msgType {
	return msgRxattrcreate
}

// String implements fmt.Stringer.
func (r *rxattrcreate) String() string {
	return fmt.Sprintf("Rxattrcreate{}")
}

// treaddir is a readdir request.
type treaddir struct {
	// Directory is the directory FID to read.
	Directory FID

	// Offset is the offset to read at.
	Offset uint64

	// Count is the number of bytes to read.
	Count uint32
}

// Decode implements encoder.Decode.
func (t *treaddir) Decode(b *buffer) {
	t.Directory = b.ReadFID()
	t.Offset = b.Read64()
	t.Count = b.Read32()
}

// Encode implements encoder.Encode.
func (t *treaddir) Encode(b *buffer) {
	b.WriteFID(t.Directory)
	b.Write64(t.Offset)
	b.Write32(t.Count)
}

// Type implements message.Type.
func (*treaddir) Type() msgType {
	return msgTreaddir
}

// String implements fmt.Stringer.
func (t *treaddir) String() string {
	return fmt.Sprintf("Treaddir{DirectoryFID: %d, Offset: %d, Count: %d}", t.Directory, t.Offset, t.Count)
}

// rreaddir is a readdir response.
type rreaddir struct {
	// Count is the byte limit.
	//
	// This should always be set from the Treaddir request.
	Count uint32

	// Entries are the resulting entries.
	//
	// This may be constructed in decode.
	Entries []Dirent

	// payload is the encoded payload.
	//
	// This is constructed by encode.
	payload []byte
}

// Decode implements encoder.Decode.
func (r *rreaddir) Decode(b *buffer) {
	r.Count = b.Read32()
	entriesBuf := buffer{data: r.payload}
	r.Entries = r.Entries[:0]
	for {
		var d Dirent
		d.Decode(&entriesBuf)
		if entriesBuf.isOverrun() {
			// Couldn't decode a complete entry.
			break
		}
		r.Entries = append(r.Entries, d)
	}
}

// Encode implements encoder.Encode.
func (r *rreaddir) Encode(b *buffer) {
	entriesBuf := buffer{}
	for _, d := range r.Entries {
		d.Encode(&entriesBuf)
		if len(entriesBuf.data) >= int(r.Count) {
			break
		}
	}
	if len(entriesBuf.data) < int(r.Count) {
		r.Count = uint32(len(entriesBuf.data))
		r.payload = entriesBuf.data
	} else {
		r.payload = entriesBuf.data[:r.Count]
	}
	b.Write32(uint32(r.Count))
}

// Type implements message.Type.
func (*rreaddir) Type() msgType {
	return msgRreaddir
}

// FixedSize implements payloader.FixedSize.
func (*rreaddir) FixedSize() uint32 {
	return 4
}

// Payload implements payloader.Payload.
func (r *rreaddir) Payload() []byte {
	return r.payload
}

// SetPayload implements payloader.SetPayload.
func (r *rreaddir) SetPayload(p []byte) {
	r.payload = p
}

// String implements fmt.Stringer.
func (r *rreaddir) String() string {
	return fmt.Sprintf("Rreaddir{Count: %d, Entries: %s}", r.Count, r.Entries)
}

// Tfsync is an fsync request.
type tfsync struct {
	// FID is the fid to sync.
	FID FID
}

// Decode implements encoder.Decode.
func (t *tfsync) Decode(b *buffer) {
	t.FID = b.ReadFID()
}

// Encode implements encoder.Encode.
func (t *tfsync) Encode(b *buffer) {
	b.WriteFID(t.FID)
}

// Type implements message.Type.
func (*tfsync) Type() msgType {
	return msgTfsync
}

// String implements fmt.Stringer.
func (t *tfsync) String() string {
	return fmt.Sprintf("Tfsync{FID: %d}", t.FID)
}

// rfsync is an fsync response.
type rfsync struct {
}

// Decode implements encoder.Decode.
func (*rfsync) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rfsync) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rfsync) Type() msgType {
	return msgRfsync
}

// String implements fmt.Stringer.
func (r *rfsync) String() string {
	return fmt.Sprintf("Rfsync{}")
}

// tstatfs is a stat request.
type tstatfs struct {
	// FID is the root.
	FID FID
}

// Decode implements encoder.Decode.
func (t *tstatfs) Decode(b *buffer) {
	t.FID = b.ReadFID()
}

// Encode implements encoder.Encode.
func (t *tstatfs) Encode(b *buffer) {
	b.WriteFID(t.FID)
}

// Type implements message.Type.
func (*tstatfs) Type() msgType {
	return msgTstatfs
}

// String implements fmt.Stringer.
func (t *tstatfs) String() string {
	return fmt.Sprintf("Tstatfs{FID: %d}", t.FID)
}

// rstatfs is the response for a Tstatfs.
type rstatfs struct {
	// FSStat is the stat result.
	FSStat FSStat
}

// Decode implements encoder.Decode.
func (r *rstatfs) Decode(b *buffer) {
	r.FSStat.Decode(b)
}

// Encode implements encoder.Encode.
func (r *rstatfs) Encode(b *buffer) {
	r.FSStat.Encode(b)
}

// Type implements message.Type.
func (*rstatfs) Type() msgType {
	return msgRstatfs
}

// String implements fmt.Stringer.
func (r *rstatfs) String() string {
	return fmt.Sprintf("Rstatfs{FSStat: %v}", r.FSStat)
}

// tflushf is a flush file request, not to be confused with tflush.
type tflushf struct {
	// FID is the FID to be flushed.
	FID FID
}

// Decode implements encoder.Decode.
func (t *tflushf) Decode(b *buffer) {
	t.FID = b.ReadFID()
}

// Encode implements encoder.Encode.
func (t *tflushf) Encode(b *buffer) {
	b.WriteFID(t.FID)
}

// Type implements message.Type.
func (*tflushf) Type() msgType {
	return msgTflushf
}

// String implements fmt.Stringer.
func (t *tflushf) String() string {
	return fmt.Sprintf("Tflushf{FID: %d}", t.FID)
}

// rflushf is a flush file response.
type rflushf struct {
}

// Decode implements encoder.Decode.
func (*rflushf) Decode(b *buffer) {
}

// Encode implements encoder.Encode.
func (*rflushf) Encode(b *buffer) {
}

// Type implements message.Type.
func (*rflushf) Type() msgType {
	return msgRflushf
}

// String implements fmt.Stringer.
func (*rflushf) String() string {
	return fmt.Sprintf("Rflushf{}")
}

// twalkgetattr is a walk request.
type twalkgetattr struct {
	// FID is the FID to be walked.
	FID FID

	// NewFID is the resulting FID.
	NewFID FID

	// Names are the set of names to be walked.
	Names []string
}

// Decode implements encoder.Decode.
func (t *twalkgetattr) Decode(b *buffer) {
	t.FID = b.ReadFID()
	t.NewFID = b.ReadFID()
	n := b.Read16()
	t.Names = t.Names[:0]
	for i := 0; i < int(n); i++ {
		t.Names = append(t.Names, b.ReadString())
	}
}

// Encode implements encoder.Encode.
func (t *twalkgetattr) Encode(b *buffer) {
	b.WriteFID(t.FID)
	b.WriteFID(t.NewFID)
	b.Write16(uint16(len(t.Names)))
	for _, name := range t.Names {
		b.WriteString(name)
	}
}

// Type implements message.Type.
func (*twalkgetattr) Type() msgType {
	return msgTwalkgetattr
}

// String implements fmt.Stringer.
func (t *twalkgetattr) String() string {
	return fmt.Sprintf("Twalkgetattr{FID: %d, NewFID: %d, Names: %v}", t.FID, t.NewFID, t.Names)
}

// rwalkgetattr is a walk response.
type rwalkgetattr struct {
	// Valid indicates which fields are valid in the Attr below.
	Valid AttrMask

	// Attr is the set of attributes for the last QID (the file walked to).
	Attr Attr

	// QIDs are the set of QIDs returned.
	QIDs []QID
}

// Decode implements encoder.Decode.
func (r *rwalkgetattr) Decode(b *buffer) {
	r.Valid.Decode(b)
	r.Attr.Decode(b)
	n := b.Read16()
	r.QIDs = r.QIDs[:0]
	for i := 0; i < int(n); i++ {
		var q QID
		q.Decode(b)
		r.QIDs = append(r.QIDs, q)
	}
}

// Encode implements encoder.Encode.
func (r *rwalkgetattr) Encode(b *buffer) {
	r.Valid.Encode(b)
	r.Attr.Encode(b)
	b.Write16(uint16(len(r.QIDs)))
	for _, q := range r.QIDs {
		q.Encode(b)
	}
}

// Type implements message.Type.
func (*rwalkgetattr) Type() msgType {
	return msgRwalkgetattr
}

// String implements fmt.Stringer.
func (r *rwalkgetattr) String() string {
	return fmt.Sprintf("Rwalkgetattr{Valid: %s, Attr: %s, QIDs: %v}", r.Valid, r.Attr, r.QIDs)
}

// tucreate is a tlcreate message that includes a UID.
type tucreate struct {
	tlcreate

	// UID is the UID to use as the effective UID in creation messages.
	UID UID
}

// Decode implements encoder.Decode.
func (t *tucreate) Decode(b *buffer) {
	t.tlcreate.Decode(b)
	t.UID = b.ReadUID()
}

// Encode implements encoder.Encode.
func (t *tucreate) Encode(b *buffer) {
	t.tlcreate.Encode(b)
	b.WriteUID(t.UID)
}

// Type implements message.Type.
func (t *tucreate) Type() msgType {
	return msgTucreate
}

// String implements fmt.Stringer.
func (t *tucreate) String() string {
	return fmt.Sprintf("Tucreate{Tlcreate: %v, UID: %d}", &t.tlcreate, t.UID)
}

// rucreate is a file creation response.
type rucreate struct {
	rlcreate
}

// Type implements message.Type.
func (*rucreate) Type() msgType {
	return msgRucreate
}

// String implements fmt.Stringer.
func (r *rucreate) String() string {
	return fmt.Sprintf("Rucreate{%v}", &r.rlcreate)
}

// tumkdir is a Tmkdir message that includes a UID.
type tumkdir struct {
	tmkdir

	// UID is the UID to use as the effective UID in creation messages.
	UID UID
}

// Decode implements encoder.Decode.
func (t *tumkdir) Decode(b *buffer) {
	t.tmkdir.Decode(b)
	t.UID = b.ReadUID()
}

// Encode implements encoder.Encode.
func (t *tumkdir) Encode(b *buffer) {
	t.tmkdir.Encode(b)
	b.WriteUID(t.UID)
}

// Type implements message.Type.
func (t *tumkdir) Type() msgType {
	return msgTumkdir
}

// String implements fmt.Stringer.
func (t *tumkdir) String() string {
	return fmt.Sprintf("Tumkdir{Tmkdir: %v, UID: %d}", &t.tmkdir, t.UID)
}

// rumkdir is a umkdir response.
type rumkdir struct {
	rmkdir
}

// Type implements message.Type.
func (*rumkdir) Type() msgType {
	return msgRumkdir
}

// String implements fmt.Stringer.
func (r *rumkdir) String() string {
	return fmt.Sprintf("Rumkdir{%v}", &r.rmkdir)
}

// tumknod is a Tmknod message that includes a UID.
type tumknod struct {
	tmknod

	// UID is the UID to use as the effective UID in creation messages.
	UID UID
}

// Decode implements encoder.Decode.
func (t *tumknod) Decode(b *buffer) {
	t.tmknod.Decode(b)
	t.UID = b.ReadUID()
}

// Encode implements encoder.Encode.
func (t *tumknod) Encode(b *buffer) {
	t.tmknod.Encode(b)
	b.WriteUID(t.UID)
}

// Type implements message.Type.
func (t *tumknod) Type() msgType {
	return msgTumknod
}

// String implements fmt.Stringer.
func (t *tumknod) String() string {
	return fmt.Sprintf("Tumknod{Tmknod: %v, UID: %d}", &t.tmknod, t.UID)
}

// rumknod is a umknod response.
type rumknod struct {
	rmknod
}

// Type implements message.Type.
func (*rumknod) Type() msgType {
	return msgRumknod
}

// String implements fmt.Stringer.
func (r *rumknod) String() string {
	return fmt.Sprintf("Rumknod{%v}", &r.rmknod)
}

// tusymlink is a Tsymlink message that includes a UID.
type tusymlink struct {
	tsymlink

	// UID is the UID to use as the effective UID in creation messages.
	UID UID
}

// Decode implements encoder.Decode.
func (t *tusymlink) Decode(b *buffer) {
	t.tsymlink.Decode(b)
	t.UID = b.ReadUID()
}

// Encode implements encoder.Encode.
func (t *tusymlink) Encode(b *buffer) {
	t.tsymlink.Encode(b)
	b.WriteUID(t.UID)
}

// Type implements message.Type.
func (t *tusymlink) Type() msgType {
	return msgTusymlink
}

// String implements fmt.Stringer.
func (t *tusymlink) String() string {
	return fmt.Sprintf("Tusymlink{Tsymlink: %v, UID: %d}", &t.tsymlink, t.UID)
}

// rusymlink is a usymlink response.
type rusymlink struct {
	rsymlink
}

// Type implements message.Type.
func (*rusymlink) Type() msgType {
	return msgRusymlink
}

// String implements fmt.Stringer.
func (r *rusymlink) String() string {
	return fmt.Sprintf("Rusymlink{%v}", &r.rsymlink)
}

const maxCacheSize = 3

// msgFactory is used to reduce allocations by caching messages for reuse.
type msgFactory struct {
	create func() message
	cache  chan message
}

// msgRegistry indexes all message factories by type.
var msgRegistry registry

type registry struct {
	factories [math.MaxUint8]msgFactory

	// largestFixedSize is computed so that given some message size M, you can
	// compute the maximum payload size (e.g. for Twrite, Rread) with
	// M-largestFixedSize. You could do this individual on a per-message basis,
	// but it's easier to compute a single maximum safe payload.
	largestFixedSize uint32
}

// get returns a new message by type.
//
// An error is returned in the case of an unknown message.
//
// This takes, and ignores, a message tag so that it may be used directly as a
// lookupTagAndType function for recv (by design).
func (r *registry) get(_ Tag, t msgType) (message, error) {
	entry := &r.factories[t]
	if entry.create == nil {
		return nil, &ErrInvalidMsgType{t}
	}

	select {
	case msg := <-entry.cache:
		return msg, nil
	default:
		return entry.create(), nil
	}
}

func (r *registry) put(msg message) {
	if p, ok := msg.(payloader); ok {
		p.SetPayload(nil)
	}

	entry := &r.factories[msg.Type()]
	select {
	case entry.cache <- msg:
	default:
	}
}

// register registers the given message type.
//
// This may cause panic on failure and should only be used from init.
func (r *registry) register(t msgType, fn func() message) {
	if int(t) >= len(r.factories) {
		panic(fmt.Sprintf("message type %d is too large. It must be smaller than %d", t, len(r.factories)))
	}
	if r.factories[t].create != nil {
		panic(fmt.Sprintf("duplicate message type %d: first is %T, second is %T", t, r.factories[t].create(), fn()))
	}
	r.factories[t] = msgFactory{
		create: fn,
		cache:  make(chan message, maxCacheSize),
	}

	if size := calculateSize(fn()); size > r.largestFixedSize {
		r.largestFixedSize = size
	}
}

func calculateSize(m message) uint32 {
	if p, ok := m.(payloader); ok {
		return p.FixedSize()
	}
	var dataBuf buffer
	m.Encode(&dataBuf)
	return uint32(len(dataBuf.data))
}

func init() {
	msgRegistry.register(msgRlerror, func() message { return &rlerror{} })
	msgRegistry.register(msgTstatfs, func() message { return &tstatfs{} })
	msgRegistry.register(msgRstatfs, func() message { return &rstatfs{} })
	msgRegistry.register(msgTlopen, func() message { return &tlopen{} })
	msgRegistry.register(msgRlopen, func() message { return &rlopen{} })
	msgRegistry.register(msgTlcreate, func() message { return &tlcreate{} })
	msgRegistry.register(msgRlcreate, func() message { return &rlcreate{} })
	msgRegistry.register(msgTsymlink, func() message { return &tsymlink{} })
	msgRegistry.register(msgRsymlink, func() message { return &rsymlink{} })
	msgRegistry.register(msgTmknod, func() message { return &tmknod{} })
	msgRegistry.register(msgRmknod, func() message { return &rmknod{} })
	msgRegistry.register(msgTrename, func() message { return &trename{} })
	msgRegistry.register(msgRrename, func() message { return &rrename{} })
	msgRegistry.register(msgTreadlink, func() message { return &treadlink{} })
	msgRegistry.register(msgRreadlink, func() message { return &rreadlink{} })
	msgRegistry.register(msgTgetattr, func() message { return &tgetattr{} })
	msgRegistry.register(msgRgetattr, func() message { return &rgetattr{} })
	msgRegistry.register(msgTsetattr, func() message { return &tsetattr{} })
	msgRegistry.register(msgRsetattr, func() message { return &rsetattr{} })
	msgRegistry.register(msgTxattrwalk, func() message { return &txattrwalk{} })
	msgRegistry.register(msgRxattrwalk, func() message { return &rxattrwalk{} })
	msgRegistry.register(msgTxattrcreate, func() message { return &txattrcreate{} })
	msgRegistry.register(msgRxattrcreate, func() message { return &rxattrcreate{} })
	msgRegistry.register(msgTreaddir, func() message { return &treaddir{} })
	msgRegistry.register(msgRreaddir, func() message { return &rreaddir{} })
	msgRegistry.register(msgTfsync, func() message { return &tfsync{} })
	msgRegistry.register(msgRfsync, func() message { return &rfsync{} })
	msgRegistry.register(msgTlink, func() message { return &tlink{} })
	msgRegistry.register(msgRlink, func() message { return &rlink{} })
	msgRegistry.register(msgTmkdir, func() message { return &tmkdir{} })
	msgRegistry.register(msgRmkdir, func() message { return &rmkdir{} })
	msgRegistry.register(msgTrenameat, func() message { return &trenameat{} })
	msgRegistry.register(msgRrenameat, func() message { return &rrenameat{} })
	msgRegistry.register(msgTunlinkat, func() message { return &tunlinkat{} })
	msgRegistry.register(msgRunlinkat, func() message { return &runlinkat{} })
	msgRegistry.register(msgTversion, func() message { return &tversion{} })
	msgRegistry.register(msgRversion, func() message { return &rversion{} })
	msgRegistry.register(msgTauth, func() message { return &tauth{} })
	msgRegistry.register(msgRauth, func() message { return &rauth{} })
	msgRegistry.register(msgTattach, func() message { return &tattach{} })
	msgRegistry.register(msgRattach, func() message { return &rattach{} })
	msgRegistry.register(msgTflush, func() message { return &tflush{} })
	msgRegistry.register(msgRflush, func() message { return &rflush{} })
	msgRegistry.register(msgTwalk, func() message { return &twalk{} })
	msgRegistry.register(msgRwalk, func() message { return &rwalk{} })
	msgRegistry.register(msgTread, func() message { return &tread{} })
	msgRegistry.register(msgRread, func() message { return &rread{} })
	msgRegistry.register(msgTwrite, func() message { return &twrite{} })
	msgRegistry.register(msgRwrite, func() message { return &rwrite{} })
	msgRegistry.register(msgTclunk, func() message { return &tclunk{} })
	msgRegistry.register(msgRclunk, func() message { return &rclunk{} })
	msgRegistry.register(msgTremove, func() message { return &tremove{} })
	msgRegistry.register(msgRremove, func() message { return &rremove{} })
	msgRegistry.register(msgTflushf, func() message { return &tflushf{} })
	msgRegistry.register(msgRflushf, func() message { return &rflushf{} })
	msgRegistry.register(msgTwalkgetattr, func() message { return &twalkgetattr{} })
	msgRegistry.register(msgRwalkgetattr, func() message { return &rwalkgetattr{} })
	msgRegistry.register(msgTucreate, func() message { return &tucreate{} })
	msgRegistry.register(msgRucreate, func() message { return &rucreate{} })
	msgRegistry.register(msgTumkdir, func() message { return &tumkdir{} })
	msgRegistry.register(msgRumkdir, func() message { return &rumkdir{} })
	msgRegistry.register(msgTumknod, func() message { return &tumknod{} })
	msgRegistry.register(msgRumknod, func() message { return &rumknod{} })
	msgRegistry.register(msgTusymlink, func() message { return &tusymlink{} })
	msgRegistry.register(msgRusymlink, func() message { return &rusymlink{} })
	msgRegistry.register(msgTallocate, func() message { return &tallocate{} })
	msgRegistry.register(msgRallocate, func() message { return &rallocate{} })
}
