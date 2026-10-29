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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxadm/tail"
)

func serialTestTail(t *testing.T, path string) *tail.Tail {
	t.Helper()
	reader, err := tail.TailFile(path, tail.Config{
		Location:      &tail.SeekInfo{Offset: 0},
		Follow:        true,
		CompleteLines: true,
		MustExist:     false,
		ReOpen:        true,
		Logger:        tail.DiscardingLogger,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Stop(); err != nil {
			t.Error(err)
		}
		reader.Cleanup()
	})
	return reader
}

func serialTestTailLine(t *testing.T, reader *tail.Tail, expected string) {
	t.Helper()
	select {
	case line := <-reader.Lines:
		if line == nil || line.Err != nil || line.Text != expected {
			t.Fatalf("sidecar line: got %+v, want %q", line, expected)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion marker did not reach sidecar tail configuration")
	}
}

func TestOpenVMMSerialDetachedMarkerReachesSidecarTail(t *testing.T) {
	_, backend, directory, _ := serialTestBroker(t, true)
	marker := "COMPLETE RunId=no-console Result=SUCCESS"
	serialTestWrite(t, backend, []byte(marker+"\n"))
	serialTestLog(t, directory, []byte(marker+"\n"))
	reader := serialTestTail(t, filepath.Join(directory, "virt-serial0-log"))
	serialTestTailLine(t, reader, marker)
}

func TestOpenVMMSerialFragmentedMarkerSurvivesRotation(t *testing.T) {
	path := filepath.Join(serialTestDirectory(t), "virt-serial0-log")
	writer, err := newOpenVMMSerialLog(path, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.maxSize = 128
	reader := serialTestTail(t, path)
	before := strings.Repeat("x", 110)
	if _, err := writer.Write([]byte(before + "\n")); err != nil {
		t.Fatal(err)
	}
	serialTestTailLine(t, reader, before)
	if _, err := writer.Write([]byte("COMPLETE RunId=rotation ")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("Result=SUCCESS\n")); err != nil {
		t.Fatal(err)
	}
	serialTestTailLine(t, reader, "COMPLETE RunId=rotation Result=SUCCESS")
}
