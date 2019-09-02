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
//     local_server 127.0.0.1:3333
//
// Then, connect using the Linux 9P filesystem:
//     mount -t 9p -o trans=tcp,port=3333 127.0.0.1 /mnt
package main

import (
	"flag"
	"log"
	"net"
	"os"

	"github.com/hugelgupf/p9/localfs"
	"github.com/hugelgupf/p9/p9"
)

var (
	verbose = flag.Bool("v", false, "verbose logging")
	root    = flag.String("root", "/", "root dir of file system to expose")
)

func main() {
	flag.Parse()
	if *verbose {
		p9.Debug = log.Printf
	}

	if len(os.Args) != 2 {
		log.Fatalf("usage: %s <bind-addr>", os.Args[0])
	}

	// Bind and listen on the socket.
	serverSocket, err := net.Listen("tcp", os.Args[1])
	if err != nil {
		log.Fatalf("err binding: %v", err)
	}

	// Run the server.
	s := p9.NewServer(localfs.Attacher(*root))
	s.Serve(serverSocket)
}
