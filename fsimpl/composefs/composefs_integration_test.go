//go:build !race && linux
// +build !race,linux

package composefs

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/localfs"
	"github.com/hugelgupf/p9/fsimpl/staticfs"
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
	defer serverSocket.Close()
	serverPort := serverSocket.Addr().(*net.TCPAddr).Port

	localfsTmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(localfsTmp, "somefile"), []byte("hahaha"), 0777); err != nil {
		t.Fatal(err)
	}

	attacher, err := New(
		WithFile("foo.txt", staticfs.ReadOnlyFile("barbarbar")),
		WithFile("baz.txt", staticfs.ReadOnlyFile("barbarbarbar")),
		WithMount("localfs", localfs.Attacher(localfsTmp)),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := rovmtests.Expectations{
		Dirs: []rovmtests.Dir{
			{Path: "", Members: []string{"foo.txt", "baz.txt", "localfs"}},
			{Path: "localfs", Members: []string{"somefile"}},
		},
		Files: []rovmtests.File{
			{Path: "foo.txt", Content: "barbarbar"},
			{Path: "baz.txt", Content: "barbarbarbar"},
			{Path: "localfs/somefile", Content: "hahaha"},
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
	go s.Serve(serverSocket)

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
			},
		},
	})
}
