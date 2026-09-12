package provider

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// ExecBarrier runs only in the private trampoline child. No provider instruction
// can run until its owner has armed observation and releases fd 3.
func ExecBarrier(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing provider executable")
	}
	barrier := os.NewFile(3, "provider-start-barrier")
	if barrier == nil {
		return fmt.Errorf("missing provider start barrier")
	}
	var b [1]byte
	_, err := io.ReadFull(barrier, b[:])
	_ = barrier.Close()
	if err != nil || b[0] != 1 {
		return fmt.Errorf("provider start barrier was not released")
	}
	return syscall.Exec(args[0], args, os.Environ())
}

// A kernel event filter is armed before exec; snapshots cannot supply this proof.
var armProcess = func(pid int) (int, error) {
	fd, err := unix.Kqueue()
	if err != nil {
		return -1, err
	}
	event := unix.Kevent_t{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ENABLE | unix.EV_CLEAR, Fflags: unix.NOTE_FORK | unix.NOTE_EXEC | unix.NOTE_EXIT}
	if _, err = unix.Kevent(fd, []unix.Kevent_t{event}, nil, nil); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func collectProcess(fd int, proof *ProcessProof) error {
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(fd, nil, events, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n != 1 || events[0].Flags&unix.EV_ERROR != 0 {
			return fmt.Errorf("process observation is incomplete")
		}
		flags := events[0].Fflags
		proof.ExecObserved = proof.ExecObserved || flags&unix.NOTE_EXEC != 0
		proof.ForkObserved = proof.ForkObserved || flags&unix.NOTE_FORK != 0
		if flags&unix.NOTE_EXIT != 0 {
			proof.ExitObserved = true
			return nil
		}
	}
}

func observeProcess(args []string, proof *ProcessProof) (int, error) {
	executable, err := exec.LookPath(args[0])
	if err != nil {
		proof.NeverStarted = true
		return 127, err
	}
	self, err := os.Executable()
	if err != nil {
		proof.NeverStarted = true
		return 1, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		proof.NeverStarted = true
		return 1, err
	}
	defer reader.Close()
	defer writer.Close()
	argv := append([]string{"_provider-exec", executable}, args[1:]...)
	child := exec.Command(self, argv...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.ExtraFiles = []*os.File{reader}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = child.Start(); err != nil {
		proof.NeverStarted = true
		return 1, err
	}
	proof.PID = child.Process.Pid
	_ = reader.Close()
	// The owner only signals its live child; a missing owner cannot mint a proof.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	defer signal.Stop(signals)
	defer close(done)
	go func() {
		select {
		case sig := <-signals:
			_ = child.Process.Signal(sig)
		case <-done:
		}
	}()
	fd, observeErr := armProcess(child.Process.Pid)
	proof.Armed = observeErr == nil
	observed := make(chan error, 1)
	if proof.Armed {
		defer unix.Close(fd)
		go func() { observed <- collectProcess(fd, proof) }()
	}
	if _, err = writer.Write([]byte{1}); err != nil {
		_ = child.Process.Kill()
	}
	_ = writer.Close()
	waitErr := child.Wait()
	if proof.Armed {
		observeErr = <-observed
	}
	proof.Quiescent = proof.Armed && observeErr == nil && proof.ExitObserved && !proof.ForkObserved
	proof.NeverStarted = proof.Quiescent && !proof.ExecObserved
	if child.ProcessState == nil {
		proof.Quiescent, proof.NeverStarted = false, false
		return 1, waitErr
	}
	if observeErr != nil {
		return child.ProcessState.ExitCode(), observeErr
	}
	return child.ProcessState.ExitCode(), nil
}
