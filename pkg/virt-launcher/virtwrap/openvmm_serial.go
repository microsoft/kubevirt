/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright The KubeVirt Authors.
 *
 */

package virtwrap

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
)

const (
	// openVMMSerialSocket is the console socket virt-handler connects to, as on KVM.
	openVMMSerialSocket = "virt-serial0"
	// openVMMPrivateSerialSocket is where OpenVMM itself listens for COM1. Only the launcher's relay connects to it.
	openVMMPrivateSerialSocket = "openvmm-serial0"
	openVMMSerialLogSuffix     = "-log"

	// Match the virtlogd defaults that bound virt-serial0-log on KVM (max_size and max_backups in
	// https://github.com/libvirt/libvirt/blob/v12.8.0/src/logging/log_daemon_config.c).
	openVMMSerialLogMaxSize    = 2 * 1024 * 1024
	openVMMSerialLogMaxBackups = 3

	openVMMSerialDialInterval  = 10 * time.Millisecond
	openVMMSerialDialWarnAfter = 30 * time.Second
	openVMMSerialLogFileMode   = 0644
)

// openVMMSerialRelay gives OpenVMM's COM1 the behavior that KVM gets from a QEMU serial chardev with a logfile, which
// pkg/virt-launcher/virtwrap/converter/compute/console.go configures through libvirt.
//
// OpenVMM's socket serial backend serves a single client at a time and discards guest output while no client is
// connected, so it cannot both feed a log and serve virtctl console. The relay is therefore OpenVMM's only client: it
// appends all guest output to virt-serial0-log and serves virt-serial0 to one console client at a time, like QEMU.
type openVMMSerialRelay struct {
	listener net.Listener
	done     chan struct{}
	stopOnce sync.Once

	mutex  sync.Mutex
	vmm    net.Conn
	client net.Conn
}

func openVMMSerialSocketPath(consoleDir, uid string) string {
	return filepath.Join(consoleDir, uid, openVMMSerialSocket)
}

func openVMMPrivateSerialSocketPath(consoleDir, uid string) string {
	return filepath.Join(consoleDir, uid, openVMMPrivateSerialSocket)
}

func openVMMSerialLogEnabled(vmi *v1.VirtualMachineInstance, options *cmdv1.VirtualMachineOptions) bool {
	autoattach := vmi.Spec.Domain.Devices.AutoattachSerialConsole
	if autoattach != nil && !*autoattach {
		return false
	}
	return isSerialConsoleLogEnabled(options.GetClusterConfig().GetSerialConsoleLogDisabled(), vmi)
}

// startOpenVMMSerialRelay listens on socketPath for console clients and connects to OpenVMM at targetPath once it
// binds. Guest output is appended to logPath unless logPath is empty.
func startOpenVMMSerialRelay(socketPath, targetPath, logPath string) (*openVMMSerialRelay, error) {
	for _, path := range []string{socketPath, targetPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to remove stale OpenVMM serial socket %s: %w", path, err)
		}
	}

	var logWriter *rotatingFileWriter
	if logPath != "" {
		var err error
		logWriter, err = openRotatingFileWriter(logPath, openVMMSerialLogMaxSize, openVMMSerialLogMaxBackups)
		if err != nil {
			return nil, fmt.Errorf("failed to open OpenVMM serial console log %s: %w", logPath, err)
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		if logWriter != nil {
			_ = logWriter.Close()
		}
		return nil, fmt.Errorf("failed to listen on OpenVMM serial socket %s: %w", socketPath, err)
	}

	relay := &openVMMSerialRelay{listener: listener, done: make(chan struct{})}
	go relay.run(targetPath, logWriter)
	return relay, nil
}

// Stop disconnects the console client and OpenVMM. Closing the listener also removes virt-serial0.
func (r *openVMMSerialRelay) Stop() {
	r.stopOnce.Do(func() {
		r.mutex.Lock()
		defer r.mutex.Unlock()
		close(r.done)
		_ = r.listener.Close()
		for _, conn := range []net.Conn{r.client, r.vmm} {
			if conn != nil {
				_ = conn.Close()
			}
		}
	})
}

func (r *openVMMSerialRelay) run(targetPath string, logWriter *rotatingFileWriter) {
	var guestOutput io.Writer = r
	if logWriter != nil {
		defer logWriter.Close()
		guestOutput = io.MultiWriter(logWriter, r)
	}

	vmm := r.connectToOpenVMM(targetPath)
	if vmm == nil {
		return
	}
	go r.serveConsoles(vmm)
	_, _ = io.Copy(guestOutput, vmm)
}

// connectToOpenVMM waits for OpenVMM to bind its serial socket, returning nil if the relay is stopped first.
func (r *openVMMSerialRelay) connectToOpenVMM(targetPath string) net.Conn {
	warnAt := time.Now().Add(openVMMSerialDialWarnAfter)
	for {
		conn, err := net.Dial("unix", targetPath)
		if err == nil {
			if !r.track(&r.vmm, conn) {
				return nil
			}
			return conn
		}
		if !warnAt.IsZero() && time.Now().After(warnAt) {
			warnAt = time.Time{}
			log.Log.Reason(err).Warningf("OpenVMM has not opened its serial socket %s yet; still waiting", targetPath)
		}
		select {
		case <-r.done:
			return nil
		case <-time.After(openVMMSerialDialInterval):
		}
	}
}

// serveConsoles forwards input from one console client at a time. Like a QEMU socket chardev, later clients wait in
// the listen backlog until the current one disconnects; virt-handler closes its previous connection before dialing.
func (r *openVMMSerialRelay) serveConsoles(vmm net.Conn) {
	for {
		client, err := r.listener.Accept()
		if err != nil {
			return
		}
		if !r.track(&r.client, client) {
			return
		}
		_, _ = io.Copy(vmm, client)
		r.track(&r.client, nil)
		_ = client.Close()
	}
}

// track records conn in slot so Stop can close it, and closes conn instead if the relay has already stopped.
func (r *openVMMSerialRelay) track(slot *net.Conn, conn net.Conn) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	select {
	case <-r.done:
		if conn != nil {
			_ = conn.Close()
		}
		return false
	default:
		*slot = conn
		return true
	}
}

// Write sends guest output to the attached console, if any. As with KVM, a console that stops reading holds up guest
// serial output, and so the log, until it reads again or disconnects: QEMU keeps the emulated UART busy until the
// socket is writable (serial_xmit in https://github.com/qemu/qemu/blob/v11.1.0/hw/char/serial.c).
func (r *openVMMSerialRelay) Write(data []byte) (int, error) {
	// The lock only snapshots the current console; the write itself happens outside it.
	r.mutex.Lock()
	client := r.client
	r.mutex.Unlock()
	if client != nil {
		_, _ = client.Write(data)
	}
	return len(data), nil
}

// rotatingFileWriter appends to a file and rotates it like virtlogd: once the next write would exceed maxSize, path
// becomes path.0, older backups shift up by one, and the oldest beyond maxBackups is discarded. It is owned by a single
// goroutine.
type rotatingFileWriter struct {
	path       string
	maxSize    int64
	maxBackups int
	file       *os.File
	size       int64
	failed     bool
}

func openRotatingFileWriter(path string, maxSize int64, maxBackups int) (*rotatingFileWriter, error) {
	writer := &rotatingFileWriter{path: path, maxSize: maxSize, maxBackups: maxBackups}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

// open sets the mode explicitly because the launcher's umask may be stricter than 022, and the guest-console-log
// container reads the log as a different user.
func (w *rotatingFileWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, openVMMSerialLogFileMode)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err == nil {
		err = file.Chmod(openVMMSerialLogFileMode)
	}
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}

// Write never fails so that a logging problem cannot interrupt the console. Only the first error is reported, and a
// file lost during a failed rotation is reopened on the next write.
func (w *rotatingFileWriter) Write(data []byte) (int, error) {
	var errs []error
	if w.file == nil {
		errs = append(errs, w.open())
	} else if w.size > 0 && w.size+int64(len(data)) > w.maxSize {
		errs = append(errs, w.rotate())
	}
	if w.file != nil {
		n, err := w.file.Write(data)
		w.size += int64(n)
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil && !w.failed {
		w.failed = true
		log.Log.Reason(err).Errorf("failed to append to serial console log %s", w.path)
	}
	return len(data), nil
}

func (w *rotatingFileWriter) rotate() error {
	_ = w.file.Close()
	w.file = nil
	var errs []error
	for index := w.maxBackups - 1; index > 0; index-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", w.path, index-1), fmt.Sprintf("%s.%d", w.path, index)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := os.Rename(w.path, w.path+".0"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, w.open())
	return errors.Join(errs...)
}

func (w *rotatingFileWriter) Close() error {
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}
