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

// Binary p9ufs provides a local 9P2000.L server for the p9 package.
//
// To use, first start the server:
//
//	p9ufs 127.0.0.1:3333
//
// Then, connect using the Linux 9P filesystem:
//
//	mount -t 9p -o trans=tcp,port=3333 127.0.0.1 /mnt
package main

import (
	"flag"
	"fmt"
	"net"
	"os"

	"github.com/hugelgupf/p9/fsimpl/localfs"
	"github.com/hugelgupf/p9/p9"
	"github.com/u-root/uio/ulog"
)

var (
	verbose = flag.Bool("v", false, "verbose logging")
	root    = flag.String("root", "/", "root dir of file system to expose")
	unix    = flag.Bool("unix", false, "use unix domain socket instead of TCP")
)

// Prints custom help to document addr:port argument
func Usage() {
	fmt.Print("p9ufs - local 9P2000.L server in userspace\n\n")
	fmt.Printf("usage: %s [options] <bind-addr:port>\n\noptions:\n", os.Args[0])
	// print options to stdout
	flag.CommandLine.SetOutput(os.Stdout)
	flag.PrintDefaults()
}

func main() {
	// 'flag' setup
	// - disable flag usage to avoid printing it after errors
	flag.Usage = func() {}
	// - return errors to handle them manually
	flag.CommandLine.Init("p9ufs", flag.ContinueOnError)
	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		// error is already printed to stderr at this point
		if err == flag.ErrHelp {
			// process -h, --help
			Usage()
			os.Exit(0)
		}
		os.Exit(1)
	}
	// - print usage if no params given
	if len(flag.Args()) != 1 {
		Usage()
		os.Exit(0)
	}

	var network string
	if *unix {
		network = "unix"
	} else {
		network = "tcp"
	}

	// Bind and listen on the socket.
	serverSocket, err := net.Listen(network, flag.Args()[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "err binding: %v\n", err)
		os.Exit(2)
	}

	var opts []p9.ServerOpt
	if *verbose {
		opts = append(opts, p9.WithServerLogger(ulog.Log))
	}
	// Run the server.
	s := p9.NewServer(localfs.Attacher(*root), opts...)
	s.Serve(serverSocket)
}
