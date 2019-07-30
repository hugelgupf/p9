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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"sync"
	"syscall"

	"github.com/hugelgupf/p9/vecnet"
)

// ErrSocket is returned in cases of a socket issue.
//
// This may be treated differently than other errors.
type ErrSocket struct {
	// error is the socket error.
	error
}

func (e ErrSocket) Error() string {
	return fmt.Sprintf("socket error: %v", e.error)
}

// ErrMessageTooLarge indicates the size was larger than reasonable.
type ErrMessageTooLarge struct {
	size  uint32
	msize uint32
}

// Error returns a sensible error.
func (e *ErrMessageTooLarge) Error() string {
	return fmt.Sprintf("message too large for fixed buffer: size is %d, limit is %d", e.size, e.msize)
}

// ErrNoValidMessage indicates no valid message could be decoded.
var ErrNoValidMessage = errors.New("buffer contained no valid message")

const (
	// headerLength is the number of bytes required for a header.
	headerLength uint32 = 7

	// maximumLength is the largest possible message.
	maximumLength uint32 = 4 * 1024 * 1024

	// initialBufferLength is the initial data buffer we allocate.
	initialBufferLength uint32 = 64
)

var dataPool = sync.Pool{
	New: func() interface{} {
		// These buffers are used for decoding without a payload.
		return make([]byte, initialBufferLength)
	},
}

// send sends the given message over the socket.
func send(conn net.Conn, tag Tag, m message) error {
	data := dataPool.Get().([]byte)
	dataBuf := buffer{data: data[:0]}

	Debug("send [conn %p] [Tag %06d] %s", conn, tag, m)

	// Encode the message. The buffer will grow automatically.
	m.Encode(&dataBuf)

	// Get our vectors to send.
	var hdr [headerLength]byte
	vecs := make(net.Buffers, 0, 3)
	vecs = append(vecs, hdr[:])
	if len(dataBuf.data) > 0 {
		vecs = append(vecs, dataBuf.data)
	}
	totalLength := headerLength + uint32(len(dataBuf.data))

	// Is there a payload?
	if payloader, ok := m.(payloader); ok {
		p := payloader.Payload()
		if len(p) > 0 {
			vecs = append(vecs, p)
			totalLength += uint32(len(p))
		}
	}

	// Construct the header.
	headerBuf := buffer{data: hdr[:0]}
	headerBuf.Write32(totalLength)
	headerBuf.WriteMsgType(m.Type())
	headerBuf.WriteTag(tag)

	if _, err := vecs.WriteTo(conn); err != nil {
		return ErrSocket{err}
	}

	// All set.
	dataPool.Put(dataBuf.data)
	return nil
}

// lookupTagAndType looks up an existing message or creates a new one.
//
// This is called by recv after decoding the header. Any error returned will be
// propagating back to the caller. You may use messageByType directly as a
// lookupTagAndType function (by design).
type lookupTagAndType func(tag Tag, t MsgType) (message, error)

// recv decodes a message from the socket.
//
// This is done in two parts, and is thus not safe for multiple callers.
//
// On a socket error, the special error type ErrSocket is returned.
//
// The tag value NoTag will always be returned if err is non-nil.
func recv(conn net.Conn, msize uint32, lookup lookupTagAndType) (Tag, message, error) {
	// Read a header.
	var hdr [headerLength]byte

	if _, err := io.ReadAtLeast(conn, hdr[:], int(headerLength)); err != nil {
		return NoTag, nil, ErrSocket{err}
	}

	// Decode the header.
	headerBuf := buffer{data: hdr[:]}
	size := headerBuf.Read32()
	t := headerBuf.ReadMsgType()
	tag := headerBuf.ReadTag()
	if size < headerLength {
		// The message is too small.
		//
		// See above: it's probably screwed.
		return NoTag, nil, ErrSocket{ErrNoValidMessage}
	}
	if size > maximumLength || size > msize {
		// The message is too big.
		return NoTag, nil, ErrSocket{&ErrMessageTooLarge{size, msize}}
	}
	remaining := size - headerLength

	// Find our message to decode.
	m, err := lookup(tag, t)
	if err != nil {
		// Throw away the contents of this message.
		if remaining > 0 {
			io.Copy(ioutil.Discard, io.LimitReader(conn, int64(remaining)))
		}
		return tag, nil, err
	}

	// Not yet initialized.
	var dataBuf buffer

	// Read the rest of the payload.
	//
	// This requires some special care to ensure that the vectors all line
	// up the way they should. We do this to minimize copying data around.
	var vecs vecnet.Buffers
	if payloader, ok := m.(payloader); ok {
		fixedSize := payloader.FixedSize()

		// Do we need more than there is?
		if fixedSize > remaining {
			// This is not a valid message.
			if remaining > 0 {
				io.Copy(ioutil.Discard, io.LimitReader(conn, int64(remaining)))
			}
			return NoTag, nil, ErrNoValidMessage
		}

		if fixedSize != 0 {
			// Pull a data buffer from the pool.
			data := dataPool.Get().([]byte)
			if int(fixedSize) > len(data) {
				// Create a larger data buffer, ensuring
				// sufficient capicity for the message.
				data = make([]byte, fixedSize)
				defer dataPool.Put(data)
				dataBuf = buffer{data: data}
				vecs = append(vecs, data)
			} else {
				// Limit the data buffer, and make sure it
				// gets filled before the payload buffer.
				defer dataPool.Put(data)
				dataBuf = buffer{data: data[:fixedSize]}
				vecs = append(vecs, data[:fixedSize])
			}
		}

		// Include the payload.
		p := payloader.Payload()
		if p == nil || len(p) != int(remaining-fixedSize) {
			p = make([]byte, remaining-fixedSize)
			payloader.SetPayload(p)
		}
		if len(p) > 0 {
			vecs = append(vecs, p)
		}
	} else if remaining != 0 {
		// Pull a data buffer from the pool.
		data := dataPool.Get().([]byte)
		if int(remaining) > len(data) {
			// Create a larger data buffer.
			data = make([]byte, remaining)
			defer dataPool.Put(data)
			dataBuf = buffer{data: data}
			vecs = append(vecs, data)
		} else {
			// Limit the data buffer.
			defer dataPool.Put(data)
			dataBuf = buffer{data: data[:remaining]}
			vecs = append(vecs, data[:remaining])
		}
	}

	if len(vecs) > 0 {
		if _, err := vecs.ReadFrom(conn.(syscall.Conn)); err != nil {
			return NoTag, nil, ErrSocket{err}
		}
	}

	// Decode the message data.
	m.Decode(&dataBuf)
	if dataBuf.isOverrun() {
		// No need to drain the socket.
		return NoTag, nil, ErrNoValidMessage
	}

	Debug("recv [conn %p] [Tag %06d] %s", conn, tag, m)

	// All set.
	return tag, m, nil
}
