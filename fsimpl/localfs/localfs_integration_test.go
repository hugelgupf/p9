//go:build !race && linux
// +build !race,linux

package localfs

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/test/vmdriver"
	"github.com/hugelgupf/p9/p9"
	"github.com/hugelgupf/vmtest"
	"github.com/hugelgupf/vmtest/qemu"
	"github.com/u-root/u-root/pkg/uroot"
	"github.com/u-root/uio/ulog/ulogtest"
)

func TestIntegration(t *testing.T) {
	serverSocket, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("err binding: %v", err)
	}
	serverPort := serverSocket.Addr().(*net.TCPAddr).Port

	// Run the server.
	tempDir := t.TempDir()
	s := p9.NewServer(Attacher(tempDir), p9.WithServerLogger(ulogtest.Logger{TB: t}))

	dd, err := exec.LookPath("dd")
	if err != nil {
		t.Errorf("Cannot run test without dd binary")
	}

	// Run the read-write tests from fsimpl/test/rwvm.
	vmtest.RunGoTestsInVM(t, []string{"github.com/hugelgupf/p9/fsimpl/test/rwvmtests"},
		vmtest.WithMergedInitramfs(uroot.Opts{
			Commands: uroot.BusyBoxCmds(
				"github.com/u-root/u-root/cmds/core/ls",
				"github.com/u-root/u-root/cmds/core/dhclient",
			),
			ExtraFiles: []string{
				dd + ":bin/dd",
			},
		}),
		vmtest.WithQEMUFn(
			qemu.WithAppendKernel(fmt.Sprintf("P9_PORT=%d P9_TARGET=192.168.0.2", serverPort)),
			// 192.168.0.0/24
			vmdriver.HostNetwork(&net.IPNet{
				IP:   net.IP{192, 168, 0, 0},
				Mask: net.CIDRMask(24, 32),
			}),
			qemu.WithVMTimeout(30*time.Second),
			qemu.WithTask(func(ctx context.Context, n *qemu.Notifications) error {
				return s.ServeContext(ctx, serverSocket)
			}),
		),
	)
}

func TestBenchmark(t *testing.T) {
	// Needs to definitely be in a tmpfs for performance testing.
	tempDir, err := ioutil.TempDir("/dev/shm", "localfs-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	serverSocket, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("err binding: %v", err)
	}
	serverPort := serverSocket.Addr().(*net.TCPAddr).Port

	// Run the server. No logger -- slows down the benchmark.
	s := p9.NewServer(Attacher(tempDir)) //, p9.WithServerLogger(ulogtest.Logger{TB: t}))

	// Run the read-write tests from fsimpl/test/rwvm.
	vmtest.RunGoTestsInVM(t, []string{"github.com/hugelgupf/p9/fsimpl/test/benchmark"},
		vmtest.WithMergedInitramfs(uroot.Opts{
			Commands: uroot.BusyBoxCmds(
				"github.com/u-root/u-root/cmds/core/ls",
				"github.com/u-root/u-root/cmds/core/dhclient",
			),
			ExtraFiles: []string{
				"/usr/bin/dd:bin/dd",
			},
		}),
		vmtest.WithQEMUFn(
			qemu.WithAppendKernel(fmt.Sprintf("P9_PORT=%d P9_TARGET=192.168.0.2", serverPort)),
			// 192.168.0.0/24
			vmdriver.HostNetwork(&net.IPNet{
				IP:   net.IP{192, 168, 0, 0},
				Mask: net.CIDRMask(24, 32),
			}),
			qemu.WithVMTimeout(30*time.Second),
			qemu.WithTask(func(ctx context.Context, n *qemu.Notifications) error {
				return s.ServeContext(ctx, serverSocket)
			}),
		),
	)
}
