// +build !race

package localfs

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/test/vmdriver"
	"github.com/hugelgupf/p9/p9"
	"github.com/u-root/u-root/pkg/qemu"
	"github.com/u-root/u-root/pkg/ulog/ulogtest"
	"github.com/u-root/u-root/pkg/uroot"
	"github.com/u-root/u-root/pkg/vmtest"
)

func TestIntegration(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "localfs-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	serverSocket, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("err binding: %v", err)
	}
	defer serverSocket.Close()
	serverPort := serverSocket.Addr().(*net.TCPAddr).Port

	// Run the server.
	s := p9.NewServer(Attacher(tempDir), p9.WithServerLogger(ulogtest.Logger{TB: t}))
	go s.Serve(serverSocket)

	// Run the read-write tests from fsimpl/test/rwvm.
	vmtest.GolangTest(t, []string{"github.com/hugelgupf/p9/fsimpl/test/rwvmtests"}, &vmtest.Options{
		BuildOpts: uroot.Opts{
			Commands: uroot.BusyBoxCmds(
				"github.com/u-root/u-root/cmds/core/dhclient",
				"github.com/u-root/u-root/cmds/core/ls",
			),
		},
		QEMUOpts: qemu.Options{
			Devices: []qemu.Device{
				vmdriver.HostNetwork{
					Net: &net.IPNet{
						// 192.168.0.0/24
						IP:   net.IP{192, 168, 0, 0},
						Mask: net.CIDRMask(24, 32),
					},
				},
			},
			KernelArgs: fmt.Sprintf("P9_PORT=%d P9_TARGET=192.168.0.2", serverPort),
			Timeout:    30 * time.Second,
		},
	})
}
