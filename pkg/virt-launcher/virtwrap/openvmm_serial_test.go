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
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
	"kubevirt.io/kubevirt/pkg/libvmi"
	"kubevirt.io/kubevirt/pkg/pointer"
)

var _ = Describe("OpenVMM serial relay", func() {
	type paths struct {
		socket string
		target string
		log    string
	}

	newPaths := func() paths {
		dir := GinkgoT().TempDir()
		return paths{
			socket: filepath.Join(dir, openVMMSerialSocket),
			target: filepath.Join(dir, openVMMPrivateSerialSocket),
			log:    filepath.Join(dir, openVMMSerialSocket+openVMMSerialLogSuffix),
		}
	}

	// startRelay binds the fake OpenVMM COM1 listener after the relay starts, as OpenVMM does, and returns the
	// relay's connection to it.
	startRelay := func(p paths, logPath string) (*openVMMSerialRelay, net.Conn) {
		relay, err := startOpenVMMSerialRelay(p.socket, p.target, logPath)
		Expect(err).ToNot(HaveOccurred(), "the relay should start listening for console clients")
		DeferCleanup(relay.Stop)

		listener, err := net.Listen("unix", p.target)
		Expect(err).ToNot(HaveOccurred(), "the fake OpenVMM serial listener should bind its socket")
		DeferCleanup(func() { _ = listener.Close() })
		Expect(listener.(*net.UnixListener).SetDeadline(time.Now().Add(5*time.Second))).To(Succeed(),
			"the fake OpenVMM listener should accept a deadline")
		vmm, err := listener.Accept()
		Expect(err).ToNot(HaveOccurred(), "the relay should connect to OpenVMM's private serial socket")
		DeferCleanup(func() { _ = vmm.Close() })
		return relay, vmm
	}

	readLog := func(path string) func() string {
		return func() string {
			contents, _ := os.ReadFile(path)
			return string(contents)
		}
	}

	expectRead := func(conn net.Conn, expected string, description string) {
		Expect(conn.SetReadDeadline(time.Now().Add(5*time.Second))).To(Succeed(), "the connection should accept a read deadline")
		received := make([]byte, len(expected))
		_, err := io.ReadFull(conn, received)
		Expect(err).ToNot(HaveOccurred(), description)
		Expect(string(received)).To(Equal(expected), description)
	}

	expectEOF := func(conn net.Conn, description string) {
		Expect(conn.SetReadDeadline(time.Now().Add(5*time.Second))).To(Succeed(), "the connection should accept a read deadline")
		_, err := conn.Read(make([]byte, 1))
		Expect(err).To(MatchError(io.EOF), description)
	}

	dialConsole := func(p paths) net.Conn {
		client, err := net.Dial("unix", p.socket)
		Expect(err).ToNot(HaveOccurred(), "a console client should be able to connect to virt-serial0")
		DeferCleanup(func() { _ = client.Close() })
		return client
	}

	send := func(conn net.Conn, data string, description string) {
		Expect(conn.SetWriteDeadline(time.Now().Add(5*time.Second))).To(Succeed(), "the connection should accept a write deadline")
		_, err := conn.Write([]byte(data))
		Expect(err).ToNot(HaveOccurred(), description)
	}

	// Console input reaching OpenVMM proves the relay is serving this client, so later guest output is routed to it.
	attachConsole := func(p paths, vmm net.Conn, probe string) net.Conn {
		client := dialConsole(p)
		send(client, probe, "the console client should be able to send input")
		expectRead(vmm, probe, "console input should be forwarded to the guest's serial port")
		return client
	}

	It("logs all guest output and serves one console at a time, like a QEMU serial chardev", func() {
		p := newPaths()
		_, vmm := startRelay(p, p.log)

		By("logging output while no console is attached")
		send(vmm, "firmware\n", "the fake guest should emit serial output")
		Eventually(readLog(p.log)).Should(Equal("firmware\n"),
			"guest output must reach virt-serial0-log even with no console client, as virtlogd does on KVM")

		By("relaying output and input for an attached console")
		first := attachConsole(p, vmm, "login\n")
		send(vmm, "prompt$ ", "the fake guest should emit serial output")
		expectRead(first, "prompt$ ", "an attached console should receive guest output")

		By("holding a second console in the backlog until the first disconnects")
		second := dialConsole(p)
		send(second, "again\n", "the waiting console should be able to queue input")
		Expect(vmm.SetReadDeadline(time.Now().Add(200*time.Millisecond))).To(Succeed(), "the fake guest should accept a read deadline")
		_, err := vmm.Read(make([]byte, 1))
		Expect(err).To(MatchError(os.ErrDeadlineExceeded), "a second console must not reach the guest while the first is attached")

		Expect(first.Close()).To(Succeed(), "the first console should disconnect cleanly")
		expectRead(vmm, "again\n", "the waiting console should be served once the first disconnects")
		send(vmm, "welcome\n", "the fake guest should emit serial output")
		expectRead(second, "welcome\n", "guest output should go to the console now being served")

		Eventually(readLog(p.log)).Should(Equal("firmware\nprompt$ welcome\n"),
			"the log should hold all guest output regardless of which console was attached")
	})

	It("serves the console without writing a log when serial console logging is disabled", func() {
		p := newPaths()
		_, vmm := startRelay(p, "")

		client := attachConsole(p, vmm, "x")
		send(vmm, "boot\n", "the fake guest should emit serial output")
		expectRead(client, "boot\n", "the console should still work with logging disabled")

		_, err := os.Stat(p.log)
		Expect(err).To(MatchError(os.ErrNotExist), "no virt-serial0-log should be created when logging is disabled")
	})

	It("stops cleanly whether or not OpenVMM has bound its serial socket", func() {
		By("abandoning the wait for OpenVMM")
		early := newPaths()
		relay, err := startOpenVMMSerialRelay(early.socket, early.target, early.log)
		Expect(err).ToNot(HaveOccurred(), "the relay should start before OpenVMM has bound its serial socket")
		relay.Stop()
		time.Sleep(5 * openVMMSerialDialInterval)
		listener, err := net.Listen("unix", early.target)
		Expect(err).ToNot(HaveOccurred(), "the fake OpenVMM serial listener should bind its socket")
		defer listener.Close()
		Expect(listener.(*net.UnixListener).SetDeadline(time.Now().Add(200*time.Millisecond))).To(Succeed(),
			"the fake OpenVMM listener should accept a deadline")
		_, err = listener.Accept()
		Expect(err).To(HaveOccurred(), "a stopped relay must not keep dialing OpenVMM's serial socket")

		By("disconnecting both sides once connected")
		p := newPaths()
		relay, vmm := startRelay(p, p.log)
		client := attachConsole(p, vmm, "x")
		relay.Stop()
		expectEOF(client, "stopping the relay should disconnect the console client")
		expectEOF(vmm, "stopping the relay should release OpenVMM's serial connection")
		_, err = os.Stat(p.socket)
		Expect(err).To(MatchError(os.ErrNotExist), "stopping the relay should remove the virt-serial0 socket")
	})

	It("holds up guest output for a console that stops reading without losing any, as KVM does", func() {
		p := newPaths()
		_, vmm := startRelay(p, p.log)
		// Far larger than a Unix socket buffer, so the guest's writes must wait for the console to read.
		bulk := strings.Repeat("z", 1024*1024)

		console := attachConsole(p, vmm, "x")
		guestDone := make(chan error, 1)
		go func() {
			_, err := vmm.Write([]byte(bulk))
			guestDone <- err
		}()
		Consistently(guestDone, 300*time.Millisecond).ShouldNot(Receive(),
			"guest output should wait while the console is not reading")

		expectRead(console, bulk, "the console should receive every byte once it reads again")
		Eventually(guestDone).Should(Receive(BeNil()), "guest output should resume once the console reads")
		Eventually(func() int { return len(readLog(p.log)()) }).Should(Equal(len(bulk)), "the log should hold every byte")
	})
	It("rotates the log like virtlogd, keeping a bounded number of readable backups", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "virt-serial0-log")
		Expect(os.WriteFile(path, []byte("old\n"), 0600)).To(Succeed(), "seeding a pre-existing log should succeed")
		// A restrictive umask must not hide rotated logs from the guest-console-log container.
		defer syscall.Umask(syscall.Umask(0077))
		writer, err := openRotatingFileWriter(path, 10, 3)
		Expect(err).ToNot(HaveOccurred(), "the writer should open an existing log for appending")
		defer writer.Close()

		// Each 8-byte write overflows the 10-byte cap, including the first, which lands on the pre-existing 4 bytes.
		for _, chunk := range []string{"aaaaaaa\n", "bbbbbbb\n", "ccccccc\n", "ddddddd\n", "eeeeeee\n"} {
			_, err := writer.Write([]byte(chunk))
			Expect(err).ToNot(HaveOccurred(), "the log writer should never fail a write")
		}

		Expect(readLog(path)()).To(Equal("eeeeeee\n"), "the live log should hold only the newest output")
		Expect(readLog(path+".0")()).To(Equal("ddddddd\n"), ".0 should be the most recent backup")
		Expect(readLog(path+".1")()).To(Equal("ccccccc\n"), ".1 should be the second most recent backup")
		Expect(readLog(path+".2")()).To(Equal("bbbbbbb\n"), ".2 should be the oldest retained backup")
		_, err = os.Stat(path + ".3")
		Expect(err).To(MatchError(os.ErrNotExist), "backups beyond the configured count should be discarded")
		for _, file := range []string{path, path + ".0"} {
			info, err := os.Stat(file)
			Expect(err).ToNot(HaveOccurred(), "%s should exist", file)
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0644)), "%s should be readable by the guest-console-log container", file)
		}

		By("recovering once a failed rotation can reopen the log")
		Expect(os.RemoveAll(dir)).To(Succeed(), "removing the log directory should succeed")
		_, err = writer.Write([]byte("lost-lost\n"))
		Expect(err).ToNot(HaveOccurred(), "a failed rotation must not fail the write")
		Expect(os.MkdirAll(dir, 0755)).To(Succeed(), "recreating the log directory should succeed")
		_, err = writer.Write([]byte("back\n"))
		Expect(err).ToNot(HaveOccurred(), "the log writer should never fail a write")
		Expect(readLog(path)()).To(Equal("back\n"), "logging should resume once the log can be reopened")
	})

	clusterOptions := func(serialConsoleLogDisabled bool) *cmdv1.VirtualMachineOptions {
		return &cmdv1.VirtualMachineOptions{
			ClusterConfig: &cmdv1.ClusterConfig{SerialConsoleLogDisabled: serialConsoleLogDisabled},
		}
	}

	DescribeTable("enables the serial console log under the same conditions as KVM",
		func(autoattach *bool, vmiLog *bool, options *cmdv1.VirtualMachineOptions, expected bool) {
			vmi := libvmi.New()
			vmi.Spec.Domain.Devices.AutoattachSerialConsole = autoattach
			vmi.Spec.Domain.Devices.LogSerialConsole = vmiLog

			Expect(openVMMSerialLogEnabled(vmi, options)).To(Equal(expected),
				"autoattach=%v logSerialConsole=%v options=%v", autoattach, vmiLog, options)
		},
		Entry("by default", nil, nil, clusterOptions(false), true),
		Entry("by default when no launcher options are provided", nil, nil, nil, true),
		Entry("not when the cluster disables it", nil, nil, clusterOptions(true), false),
		Entry("when the VMI requests it despite the cluster default", nil, pointer.P(true), clusterOptions(true), true),
		Entry("not when the VMI opts out", nil, pointer.P(false), clusterOptions(false), false),
		Entry("not when no serial console is auto-attached", pointer.P(false), pointer.P(true), clusterOptions(false), false),
	)
})
