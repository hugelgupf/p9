//go:build !race && linux
// +build !race,linux

package staticfs

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/test/rovmtests"
	"github.com/hugelgupf/p9/fsimpl/test/vmdriver"
	"github.com/hugelgupf/p9/p9"
	"github.com/hugelgupf/vmtest"
	"github.com/hugelgupf/vmtest/qemu"
	"github.com/u-root/u-root/pkg/uroot"
	"github.com/u-root/uio/ulog/ulogtest"
)

// Test that contents match when using Linux client.
func TestLinuxClient(t *testing.T) {
	serverSocket, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("err binding: %v", err)
	}
	serverPort := serverSocket.Addr().(*net.TCPAddr).Port

	wantRoot := []string{
		"foo.txt",
		"baz.txt",
	}
	longFileName := strings.Repeat("a", 200)
	opts := []Option{
		WithFile("foo.txt", "barbarbar"),
		WithFile("baz.txt", "barbarbarbar"),
	}
	// At 4096 msize, 4072 bytes will be requested for Treaddir by Linux.
	// With a file name of 200 characters, 4000 / 200 = 20 files is enough
	// to have at least 2 Rreaddir chunks.
	for i := 0; i < 20; i++ {
		opts = append(opts, WithFile(fmt.Sprintf("%s%d.txt", longFileName, i), fmt.Sprintf("file%d", i)))
		wantRoot = append(wantRoot, fmt.Sprintf("%s%d.txt", longFileName, i))
	}
	attacher, err := New(opts...)
	if err != nil {
		t.Fatal(err)
	}

	want := rovmtests.Expectations{
		Dirs: []rovmtests.Dir{
			{Path: "", Members: wantRoot},
		},
		Files: []rovmtests.File{
			{Path: "foo.txt", Content: "barbarbar"},
			{Path: "baz.txt", Content: "barbarbarbar"},
		},
	}

	dir := t.TempDir()
	wantB, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "want.json"), wantB, 0755); err != nil {
		t.Fatal(err)
	}

	// Run the server.
	s := p9.NewServer(attacher, p9.WithServerLogger(ulogtest.Logger{TB: t}))

	// Run the read tests from fsimpl/test/rovmtests.
	vmtest.RunGoTestsInVM(t, []string{"github.com/hugelgupf/p9/fsimpl/test/rovmtests"}, &vmtest.UrootFSOptions{
		BuildOpts: uroot.Opts{
			Commands: uroot.BusyBoxCmds(
				"github.com/u-root/u-root/cmds/core/dhclient",
			),
			ExtraFiles: []string{
				fmt.Sprintf("%s:etc/want.json", filepath.Join(dir, "want.json")),
			},
		},
		VMOptions: vmtest.VMOptions{
			QEMUOpts: []qemu.Fn{
				qemu.WithAppendKernel(fmt.Sprintf("P9_PORT=%d P9_TARGET=192.168.0.2", serverPort)),
				// 192.168.0.0/24
				vmdriver.HostNetwork(&net.IPNet{
					IP:   net.IP{192, 168, 0, 0},
					Mask: net.CIDRMask(24, 32),
				}),
				qemu.WithVMTimeout(30 * time.Second),
				qemu.WithTask(func(ctx context.Context, n *qemu.Notifications) error {
					return s.ServeContext(ctx, serverSocket)
				}),
			},
		},
	})
}
