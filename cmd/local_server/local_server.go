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

// Binary local_server provides a local 9P2000.L server for the p9 package.
//
// To use, first start the server:
//     local_server /tmp/my_bind_addr
//
// Then, connect using the Linux 9P filesystem:
//     mount -t 9p -o trans=unix /tmp/my_bind_addr /mnt
package main

import (
	"log"
	"os"

	"github.com/hugelgupf/p9/localfs"
	"github.com/hugelgupf/p9/p9"
	"github.com/hugelgupf/p9/unet"
)

func main() {
	if len(os.Args) != 2 {
		log.Printf("usage: %s <bind-addr>", os.Args[0])
		os.Exit(1)
	}

	// Bind and listen on the socket.
	serverSocket, err := unet.BindAndListen(os.Args[1], false)
	if err != nil {
		log.Printf("err binding: %v", err)
		os.Exit(1)
	}

	// Run the server.
	s := p9.NewServer(&localfs.Local{})
	s.Serve(serverSocket)
}
