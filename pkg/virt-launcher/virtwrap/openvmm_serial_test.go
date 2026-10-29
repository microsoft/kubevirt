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
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type serialTestReports struct {
	mutex sync.Mutex
	items []string
}

func (r *serialTestReports) add(err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.items = append(r.items, err.Error())
}

func (r *serialTestReports) contains(text string) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return strings.Contains(strings.Join(r.items, "\n"), text)
}

func serialTestDirectory(t *testing.T) string {
	t.Helper()
	// Keep Unix socket paths below sockaddr_un's limit, even with long test names.
	directory, err := os.MkdirTemp("", "kvserial-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	return directory
}

func serialTestWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(7 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("serial condition did not become true before deadline")
		}
		time.Sleep(time.Millisecond)
	}
}

func serialTestBroker(t *testing.T, capture bool) (*openVMMSerialBroker, net.Conn, string, *serialTestReports) {
	t.Helper()
	directory := serialTestDirectory(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(directory, openVMMSerialSocket), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reports := &serialTestReports{}
	broker, err := newOpenVMMSerialBroker(directory, capture, func(string) error { return nil }, reports.add)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := listener.Accept()
	if err != nil {
		broker.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = backend.Close()
		broker.Stop()
	})
	return broker, backend, directory, reports
}

func serialTestAttach(t *testing.T, broker *openVMMSerialBroker, directory string) net.Conn {
	t.Helper()
	broker.mutex.Lock()
	previous := broker.client
	broker.mutex.Unlock()
	client, err := net.DialTimeout("unix", filepath.Join(directory, "virt-serial0"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	serialTestWait(t, func() bool {
		broker.mutex.Lock()
		defer broker.mutex.Unlock()
		return broker.client != nil && broker.client != previous
	})
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return client
}

func serialTestWrite(t *testing.T, conn net.Conn, data []byte) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := conn.Write(data)
	if err != nil || n != len(data) {
		t.Fatalf("write: n=%d, err=%v", n, err)
	}
}

func serialTestRead(t *testing.T, conn net.Conn, want []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len(want))
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("got %q, want %q", data, want)
	}
}

func serialTestLog(t *testing.T, directory string, want []byte) {
	t.Helper()
	serialTestWait(t, func() bool {
		got, err := os.ReadFile(filepath.Join(directory, "virt-serial0-log"))
		return err == nil && bytes.Equal(got, want)
	})
}

func TestOpenVMMSerialDetachedCaptureAndReconnect(t *testing.T) {
	broker, backend, directory, _ := serialTestBroker(t, true)
	first := []byte("COMPLETE RunId=first Result=SUCCESS\r\n")
	serialTestWrite(t, backend, first)
	serialTestLog(t, directory, first)

	client := serialTestAttach(t, broker, directory)
	input := []byte("Get-Date\r\n")
	serialTestWrite(t, client, input)
	serialTestRead(t, backend, input)
	output := []byte{0, 1, '\r', '\n', 255}
	serialTestWrite(t, backend, output)
	serialTestRead(t, client, output)
	serialTestLog(t, directory, append(append([]byte{}, first...), output...))
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	serialTestWait(t, func() bool {
		broker.mutex.Lock()
		defer broker.mutex.Unlock()
		return broker.client == nil
	})
	last := []byte("COMPLETE RunId=detached Result=SUCCESS\n")
	serialTestWrite(t, backend, last)
	want := append(append(append([]byte{}, first...), output...), last...)
	serialTestLog(t, directory, want)
	reconnected := serialTestAttach(t, broker, directory)
	serialTestWrite(t, backend, []byte("live\n"))
	serialTestRead(t, reconnected, []byte("live\n"))
	serialTestLog(t, directory, append(want, []byte("live\n")...))
}

func TestOpenVMMSerialLoggingDisabledPreservesConsole(t *testing.T) {
	broker, backend, directory, _ := serialTestBroker(t, false)
	client := serialTestAttach(t, broker, directory)
	serialTestWrite(t, backend, []byte("live\n"))
	serialTestRead(t, client, []byte("live\n"))
	serialTestWrite(t, client, []byte("input"))
	serialTestRead(t, backend, []byte("input"))
	if _, err := os.Stat(filepath.Join(directory, "virt-serial0-log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logging disabled, stat returned %v", err)
	}
}

func TestOpenVMMSerialReplacesActiveConsole(t *testing.T) {
	broker, backend, directory, _ := serialTestBroker(t, true)
	first := serialTestAttach(t, broker, directory)
	second := serialTestAttach(t, broker, directory)
	if _, err := first.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("old console not disconnected: %v", err)
	}
	serialTestWrite(t, backend, []byte("second"))
	serialTestRead(t, second, []byte("second"))
}

func serialTestStalledClient(t *testing.T, broker *openVMMSerialBroker) *openVMMSerialClient {
	t.Helper()
	server, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	client := &openVMMSerialClient{conn: server, tx: make(chan []byte, openVMMSerialQueueSize), done: make(chan struct{})}
	broker.mutex.Lock()
	broker.client = client
	broker.workers.Add(1)
	broker.mutex.Unlock()
	go broker.writeClient(client)
	return client
}

func TestOpenVMMSerialSlowConsoleCannotBlockCapture(t *testing.T) {
	broker, backend, directory, reports := serialTestBroker(t, true)
	serialTestStalledClient(t, broker)
	payload := bytes.Repeat([]byte("guest-output\n"), 80000)
	serialTestWrite(t, backend, payload)
	serialTestLog(t, directory, payload)
	serialTestWait(t, func() bool { return reports.contains("output queue full") })
	client := serialTestAttach(t, broker, directory)
	serialTestWrite(t, backend, []byte("reconnected\n"))
	serialTestRead(t, client, []byte("reconnected\n"))
	serialTestLog(t, directory, append(payload, []byte("reconnected\n")...))
}

func TestOpenVMMSerialConsoleWriteDeadline(t *testing.T) {
	broker, backend, directory, reports := serialTestBroker(t, true)
	client := serialTestStalledClient(t, broker)
	serialTestWrite(t, backend, []byte("not blocked by console\n"))
	serialTestLog(t, directory, []byte("not blocked by console\n"))
	select {
	case <-client.done:
	case <-time.After(3 * openVMMSerialWriteTimeout):
		t.Fatal("stalled console was not disconnected")
	}
	if !reports.contains("output failure") {
		t.Fatal("console deadline failure was not reported")
	}
}

func TestOpenVMMSerialInputWriteDeadline(t *testing.T) {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	start := time.Now()
	err := writeOpenVMMSerial(server, []byte("guest not reading"))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("expected input write timeout, got %v", err)
	}
	if time.Since(start) > 3*openVMMSerialWriteTimeout {
		t.Fatal("input write exceeded bounded deadline")
	}
}

func TestOpenVMMSerialFinalDrain(t *testing.T) {
	broker, backend, directory, reports := serialTestBroker(t, true)
	stopped := make(chan struct{})
	go func() {
		broker.Stop()
		close(stopped)
	}()
	serialTestWait(t, func() bool {
		broker.mutex.Lock()
		defer broker.mutex.Unlock()
		return broker.stopping
	})
	marker := []byte("COMPLETE RunId=shutdown Result=SUCCESS\n")
	serialTestWrite(t, backend, marker)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("final drain did not join")
	}
	serialTestLog(t, directory, marker)
	if reports.contains("incomplete") {
		t.Fatal("successful EOF drain reported incomplete")
	}
	broker.Stop()
	if _, err := net.Dial("unix", filepath.Join(directory, "virt-serial0")); err == nil {
		t.Fatal("console listener remains after stop")
	}
}

func TestOpenVMMSerialDrainDeadline(t *testing.T) {
	broker, _, _, reports := serialTestBroker(t, true)
	serialTestWait(t, func() bool {
		broker.mutex.Lock()
		defer broker.mutex.Unlock()
		return broker.upstream != nil
	})
	start := time.Now()
	broker.Stop()
	if elapsed := time.Since(start); elapsed < openVMMSerialDrainTimeout || elapsed > openVMMSerialDrainTimeout+time.Second {
		t.Fatalf("unexpected drain duration: %v", elapsed)
	}
	if !reports.contains("final drain incomplete") {
		t.Fatal("drain timeout was not reported")
	}
}

func TestOpenVMMSerialTransportFailure(t *testing.T) {
	broker, backend, _, reports := serialTestBroker(t, true)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-broker.done:
	case <-time.After(time.Second):
		t.Fatal("transport failure did not clean up workers")
	}
	if !reports.contains("serial transport ended") {
		t.Fatal("transport failure was not reported")
	}
}

func TestOpenVMMSerialFileFailureIsReportedAndConsoleSurvives(t *testing.T) {
	broker, backend, directory, reports := serialTestBroker(t, true)
	writer := broker.log.(*openVMMRotatingSerialLog)
	if err := writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	client := serialTestAttach(t, broker, directory)
	serialTestWrite(t, backend, []byte("cannot persist\n"))
	serialTestRead(t, client, []byte("cannot persist\n"))
	serialTestWait(t, func() bool { return reports.contains("capture disabled after log failure") })
	serialTestWrite(t, backend, []byte("still live\n"))
	serialTestRead(t, client, []byte("still live\n"))
}

func TestOpenVMMSerialSyncAndCloseFailuresAreReported(t *testing.T) {
	broker, backend, directory, reports := serialTestBroker(t, true)
	writer := broker.log.(*openVMMRotatingSerialLog)
	serialTestWrite(t, backend, []byte("persisted\n"))
	serialTestLog(t, directory, []byte("persisted\n"))
	if err := writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-broker.done:
	case <-time.After(time.Second):
		t.Fatal("file cleanup did not finish")
	}
	if !reports.contains("sync OpenVMM serial log") || !reports.contains("close OpenVMM serial log") {
		t.Fatal("sync or close failure was hidden")
	}
}

func TestOpenVMMSerialStartupErrorsAreReturned(t *testing.T) {
	directory := serialTestDirectory(t)
	reports := &serialTestReports{}
	sentinel := errors.New("ownership denied")
	_, err := newOpenVMMSerialBroker(directory, true, func(string) error { return sentinel }, reports.add)
	if !errors.Is(err, sentinel) {
		t.Fatalf("log preparation failure hidden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "virt-serial0"), []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newOpenVMMSerialBroker(directory, true, func(string) error { return nil }, reports.add); err == nil {
		t.Fatal("console socket creation failure hidden")
	}
}

func TestOpenVMMSerialConnectCancellation(t *testing.T) {
	directory := serialTestDirectory(t)
	reports := &serialTestReports{}
	broker, err := newOpenVMMSerialBroker(directory, true, func(string) error { return nil }, reports.add)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	broker.Stop()
	if time.Since(start) > time.Second {
		t.Fatal("startup rollback waited for connection timeout")
	}
}

func TestOpenVMMSerialDelayedStartupIsReportedAndRetried(t *testing.T) {
	directory := serialTestDirectory(t)
	reports := &serialTestReports{}
	broker, err := newOpenVMMSerialBroker(directory, false, nil, reports.add)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Stop()
	serialTestWait(t, func() bool { return reports.contains("still retrying") })
	listener, err := net.Listen("unix", filepath.Join(directory, openVMMSerialSocket))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			connection <- conn
		}
	}()
	select {
	case backend := <-connection:
		defer backend.Close()
		client := serialTestAttach(t, broker, directory)
		serialTestWrite(t, backend, []byte("late startup\n"))
		serialTestRead(t, client, []byte("late startup\n"))
	case <-time.After(time.Second):
		t.Fatal("dial was not retried after startup warning")
	}
}

func TestOpenVMMSerialLogAppendAndRotationBudget(t *testing.T) {
	directory := serialTestDirectory(t)
	path := filepath.Join(directory, "virt-serial0-log")
	original := []byte("existing")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	ownershipCalls := 0
	writer, err := newOpenVMMSerialLog(path, func(path string) error {
		ownershipCalls++
		return os.Chmod(path, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.maxSize = 8
	payload := []byte("01234567abcdefghABCDEFGHijklmnopQRSTUVWX")
	n, err := writer.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("write: n=%d err=%v", n, err)
	}
	var retained []byte
	for index := openVMMSerialLogBackups - 1; index >= -1; index-- {
		name := path
		if index >= 0 {
			name += "." + strconv.Itoa(index)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 8 {
			t.Fatalf("rotation exceeded file budget: %d", len(data))
		}
		retained = append(retained, data...)
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm()&0444 != 0444 {
			t.Fatalf("rotated log is not readable: info=%v err=%v", info, err)
		}
	}
	all := append(original, payload...)
	if !bytes.Equal(retained, all[len(all)-32:]) {
		t.Fatalf("rotation changed byte order: %q", retained)
	}
	if ownershipCalls != 6 {
		t.Fatalf("ownership was not applied to each log generation: %d", ownershipCalls)
	}
}

func TestOpenVMMSerialLogOwnershipAndRotationErrors(t *testing.T) {
	directory := serialTestDirectory(t)
	path := filepath.Join(directory, "virt-serial0-log")
	sentinel := errors.New("ownership denied")
	if _, err := newOpenVMMSerialLog(path, func(string) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("ownership error hidden: %v", err)
	}
	writer, err := newOpenVMMSerialLog(path, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.maxSize = 1
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	blocker := path + "." + strconv.Itoa(openVMMSerialLogBackups-1)
	if err := os.Mkdir(blocker, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "block-removal"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if n, err := writer.Write([]byte("b")); err == nil || n != 0 {
		t.Fatalf("rotation failure returned success: n=%d err=%v", n, err)
	}
	if n, err := writer.Write([]byte("c")); err == nil || n != 0 {
		t.Fatalf("rotation failure was not sticky: n=%d err=%v", n, err)
	}
}

func TestOpenVMMSerialProductionLogBudget(t *testing.T) {
	path := filepath.Join(serialTestDirectory(t), "virt-serial0-log")
	writer, err := newOpenVMMSerialLog(path, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	payload := bytes.Repeat([]byte("x"), openVMMSerialLogMaxSize*(openVMMSerialLogBackups+1))
	if n, err := writer.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("write: n=%d err=%v", n, err)
	}
	for index := -1; index < openVMMSerialLogBackups; index++ {
		name := path
		if index >= 0 {
			name += "." + strconv.Itoa(index)
		}
		info, err := os.Stat(name)
		if err != nil || info.Size() != openVMMSerialLogMaxSize {
			t.Fatalf("production rotation budget: file=%s info=%v err=%v", name, info, err)
		}
	}
	if _, err := writer.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "y" {
		t.Fatalf("rotation did not reopen active log: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(path + "." + strconv.Itoa(openVMMSerialLogBackups)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rotation retained too many backups: %v", err)
	}
}
