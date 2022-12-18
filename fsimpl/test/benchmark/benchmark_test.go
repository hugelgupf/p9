//go:build linux

// Package benchmark_test performs a benchmark on a 9P implementation.
package benchmark_test

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"testing"

	"github.com/u-root/u-root/pkg/mount"
	"github.com/u-root/u-root/pkg/sh"
)

func TestMain(m *testing.M) {
	if os.Getuid() == 0 {
		if err := sh.RunWithLogs("dhclient", "-ipv6=false"); err != nil {
			log.Fatalf("could not configure network for tests: %v", err)
		}

		targetDir := "/target"
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(targetDir)

		mp, err := mount.Mount(os.Getenv("P9_TARGET"), targetDir, "9p", fmt.Sprintf("fscache=none,sync,trans=tcp,port=%s,msize=%d", os.Getenv("P9_PORT"), 64*1024), 0)
		if err != nil {
			log.Fatal(err)
		}
		defer mp.Unmount(0)
	}

	os.Exit(m.Run())
}

func BenchmarkReadP9UsingGo(b *testing.B) {
	//testutil.SkipIfNotRoot(b)

	src, err := os.Create("/target/zero-go-read")
	if err != nil {
		b.Error(err)
	}
	defer os.Remove(src.Name())
	defer src.Close()

	const size = 100 << 20
	const megaByte = 1 << 20

	if err := src.Truncate(size); err != nil {
		b.Error(err)
	}

	p := make([]byte, megaByte)

	b.ResetTimer()
	b.SetBytes(size)
	for i := 0; i < b.N; i++ {
		if _, err := src.Seek(0, os.SEEK_CUR); err != nil {
			b.Errorf("Seek: %v", err)
		}
		if n, err := io.CopyBuffer(io.Discard, src, p); err != nil {
			b.Errorf("Failed to copy: %v", err)
		} else if n != size {
			b.Errorf("Read %d bytes, wanted %d bytes", n, size)
		}
	}
}

func BenchmarkReadP9UsingDD(b *testing.B) {
	//testutil.SkipIfNotRoot(b)

	src, err := os.Create("/target/zero-dd-read")
	if err != nil {
		b.Error(err)
	}
	defer os.Remove(src.Name())
	defer src.Close()

	const size = 100 << 20
	const megaByte = 1 << 20

	if err := src.Truncate(size); err != nil {
		b.Error(err)
	}

	b.ResetTimer()
	b.SetBytes(size)
	for i := 0; i < b.N; i++ {
		cmd := exec.Command("dd", "if=/target/zero-dd-read", "of=/dev/null", fmt.Sprintf("bs=%d", 1<<20), fmt.Sprintf("count=%d", size/megaByte))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			b.Errorf("Error running dd: %v", err)
		}
	}
}

func BenchmarkWriteP9UsingGo(b *testing.B) {
	//testutil.SkipIfNotRoot(b)

	f, err := os.Create("/target/null-go-write")
	if err != nil {
		b.Error(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	devZero, err := os.Open("/dev/zero")
	if err != nil {
		b.Error(err)
	}
	defer devZero.Close()

	const size = 100 << 20
	const megaByte = 1 << 20

	p := make([]byte, megaByte)

	b.ResetTimer()
	b.SetBytes(size)
	for i := 0; i < b.N; i++ {
		if n, err := io.CopyBuffer(f, io.LimitReader(devZero, size), p); err != nil {
			b.Errorf("Failed to copy: %v", err)
		} else if n != size {
			b.Errorf("Wrote %d bytes, wanted %d bytes", n, size)
		}
	}
}

func BenchmarkWriteP9UsingDD(b *testing.B) {
	//testutil.SkipIfNotRoot(b)

	defer os.Remove("/target/null-dd-write")

	const size = 100 << 20
	const megaByte = 1 << 20

	b.ResetTimer()
	b.SetBytes(size)
	for i := 0; i < b.N; i++ {
		cmd := exec.Command("dd", "if=/dev/zero", "of=/target/null-dd-write", fmt.Sprintf("bs=%d", megaByte), fmt.Sprintf("count=%d", size/megaByte))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			b.Errorf("Error running dd: %v", err)
		}
	}
}
