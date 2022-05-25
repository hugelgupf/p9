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

package vecnet

import (
	"io"
	"syscall"

	"golang.org/x/sys/windows"
)

var readFromBuffers = readFromBuffersWindows

func readFromBuffersWindows(bufs Buffers, conn syscall.Conn) (int64, error) {
	rc, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}

	length := int64(0)
	for _, buf := range bufs {
		length += int64(len(buf))
	}
	for n := int64(0); n < length; {
		cur, err := recvmsg(bufs, rc)
		if err != nil && (cur == 0 || err != io.EOF) {
			return n, err
		}
		n += int64(cur)

		// Consume buffers to retry.
		for consumed := 0; consumed < cur; {
			if len(bufs[0]) <= cur-consumed {
				consumed += len(bufs[0])
				bufs = bufs[1:]
			} else {
				bufs[0] = bufs[0][cur-consumed:]
				break
			}
		}
	}
	return length, nil
}

func buildWSABufs(bufs Buffers, WSABufs []windows.WSABuf) []windows.WSABuf {
	for _, buf := range bufs {
		if l := len(buf); l > 0 {
			WSABufs = append(WSABufs, windows.WSABuf{
				Len: uint32(l),
				Buf: &buf[0],
			})
		}
	}
	return WSABufs
}

func recvmsg(bufs Buffers, rc syscall.RawConn) (int, error) {
	var (
		bytesReceived uint32
		WSABufs       = buildWSABufs(bufs, make([]windows.WSABuf, 0, 2))
		bufCount      = len(bufs)

		msg = windows.WSAMsg{
			Buffers:     &WSABufs[0],
			BufferCount: uint32(bufCount),
		}
		recvmsgCallBack = func(fd uintptr) bool {
			winErr := windows.WSARecvMsg(
				windows.Handle(fd),
				&msg, &bytesReceived,
				nil, nil) // TODO: overlapped structure?
			if winErr != nil {
				// TODO: double check documentation for other temporary issues
				// retry if err is temporary
				canRetry := (winErr == windows.WSAEINTR || winErr == windows.WSAEWOULDBLOCK)
				return !canRetry
			}
			return true
		}
		err = rc.Read(recvmsgCallBack)
	)
	return int(bytesReceived), err
}
