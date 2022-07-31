//go:build linux
// +build linux

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"syscall"

	"github.com/syndtr/gocapability/capability"
)

func Run(pivotpath *string) {
	// init
	cmd := exec.Command("/proc/self/exe", "-pivot", *pivotpath, "init")

	// clone
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWIPC |
			syscall.CLONE_NEWNET |
			syscall.CLONE_NEWNS |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWUSER |
			syscall.CLONE_NEWUTS,
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getuid(),
				Size:        1,
			},
		},
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %+v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func InitContainer(pivotpath *string) error {
	// set hostname
	if err := syscall.Sethostname([]byte("container")); err != nil {
		return fmt.Errorf("Setting hostname failed!: %w", err)
	}

	// cgroup
	if err := os.MkdirAll("/sys/fs/cgroup/my-container", 0700); err != nil {
		return fmt.Errorf("Cgroups namespace create failed!: %w", err)
	}
	if err := ioutil.WriteFile("/sys/fs/cgroup/my-container/cgroup.procs",
		[]byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
		return fmt.Errorf("Cgroup register tasks to my-container namespace failed!: %w", err)
	}
	if err := ioutil.WriteFile("/sys/fs/cgroup/my-container/cpu.max",
		[]byte("10000 100000\n"),
		0644); err != nil {
		return fmt.Errorf("Cgroup add limit cpu failed!: %w", err)
	}

	// mk proc
	if err := syscall.Mount("proc", *pivotpath+"/proc", "proc", uintptr(
		syscall.MS_NOEXEC|
			syscall.MS_NOSUID|
			syscall.MS_NODEV), ""); err != nil {
		return fmt.Errorf("Proc mount failed!: %w", err)
	}

	// mk dev
	if err := os.RemoveAll(*pivotpath + "/dev"); err != nil {
		return fmt.Errorf("Remove dev dir failed: %w", err)
	}
	if err := os.MkdirAll(*pivotpath+"/dev", 0777); err != nil {
		return fmt.Errorf("Make dev dir failed: %w", err)
	}
	for from, to := range map[string]string{
		"/proc/self/fd":   *pivotpath + "/dev/fd",
		"/proc/self/fd/0": *pivotpath + "/dev/stdin",
		"/proc/self/fd/1": *pivotpath + "/dev/stdout",
		"/proc/self/fd/2": *pivotpath + "/dev/stderr",
	} {
		if err := syscall.Symlink(from, to); err != nil {
			return fmt.Errorf("ln failed: %w", err)
		}
	}
	/*
		// mk sys
		if err := os.RemoveAll("/root/rootfs/sys"); err != nil {
			return fmt.Errorf("Remove dev dir failed: %w", err)
		}
		if err := os.MkdirAll(*pivotpath+"/sys", 0777); err != nil {
			return fmt.Errorf("Make dev dir failed: %w", err)
		}
		for from, to := range map[string]string{
			"/proc/self/fd":   *pivotpath + "/dev/fd",
			"/proc/self/fd/0": *pivotpath + "/dev/stdin",
			"/proc/self/fd/1": *pivotpath + "/dev/stdout",
			"/proc/self/fd/2": *pivotpath + "/dev/stderr",
		} {
			if err := syscall.Symlink(from, to); err != nil {
				return fmt.Errorf("ln failed: %w", err)
			}
		}
	*/
	// Chroot
	//	if err := syscall.Chroot("/root/chroot"); err != nil {
	//		return fmt.Errorf("Chroot failed!: %w", err)
	//	}

	// Pivot root
	if err := os.Chdir("/root"); err != nil {
		return fmt.Errorf("Chdir /root failed!: %w", err)
	}
	if err := syscall.Mount(*pivotpath, "/root/rootfs", "", uintptr(
		syscall.MS_BIND|
			syscall.MS_REC), ""); err != nil {
		return fmt.Errorf("Rootfs bind mount failed!: %w", err)
	}
	if err := os.MkdirAll("/root/rootfs/oldrootfs", 0700); err != nil {
		return fmt.Errorf("Oldrootfs create failed!: %w", err)
	}
	if err := syscall.PivotRoot("rootfs", "/root/rootfs/oldrootfs"); err != nil {
		return fmt.Errorf("Pivotroot failed!: %s", err)
	}
	if err := syscall.Unmount("/oldrootfs", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("Oldrootfs umount failed!: %s", err)
	}
	if err := os.RemoveAll("/oldrootfs"); err != nil {
		return fmt.Errorf("Remove oldrootfs failed: %w", err)
	}

	// process init
	if err := os.Chdir("/"); err != nil {
		return fmt.Errorf("Chdir failed!: %w", err)
	}

	// capability
	drop := []capability.Cap{
		capability.CAP_CHOWN,
	}

	caps, err := capability.NewPid2(os.Getpid())
	if err != nil {
		return fmt.Errorf("Make Capability failed!: %w", err)
	}

	caps.Unset(capability.CAPS|capability.BOUNDS, drop...)
	if caps.Apply(capability.CAPS|capability.BOUNDS) != nil {
		return fmt.Errorf("Apply Capability failed!: %w", err)
	}

	// exec
	if err := syscall.Exec("/bin/sh", []string{"/bin/sh"}, os.Environ()); err != nil {
		return fmt.Errorf("Exec failed!: %w", err)
	}
	return nil
}

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s run\n", os.Args[0])
	os.Exit(2)
}

func main() {
	var (
		pivotpath = flag.String("pivot", "/root/rootfs", "path to pivotroot")
	)
	flag.Parse()
	if len(os.Args) <= 1 {
		Usage()
	}
	switch os.Args[len(os.Args)-1] {
	case "run":
		Run(pivotpath)
	case "init":
		if err := InitContainer(pivotpath); err != nil {
			fmt.Fprintf(os.Stderr, "%+v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	default:
		Usage()
	}
}
