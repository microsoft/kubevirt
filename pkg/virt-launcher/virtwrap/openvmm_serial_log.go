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
	"fmt"
	"io"
	"os"
	"strconv"
)

// Use the virtlogd default budget: one 2 MiB file and three backups. The
// existing virt-tail sidecar follows the active pathname across renames.
const (
	openVMMSerialLogMaxSize = 2 * 1024 * 1024
	openVMMSerialLogBackups = 3
)

type openVMMRotatingSerialLog struct {
	path         string
	file         *os.File
	size         int64
	maxSize      int64
	setOwnership func(string) error
	failure      error
	lineStart    bool
}

func newOpenVMMSerialLog(path string, setOwnership func(string) error) (*openVMMRotatingSerialLog, error) {
	l := &openVMMRotatingSerialLog{path: path, maxSize: openVMMSerialLogMaxSize, setOwnership: setOwnership}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *openVMMRotatingSerialLog) open() error {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	if err := l.setOwnership(l.path); err != nil {
		return errors.Join(err, file.Close())
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(err, file.Close())
	}
	l.file = file
	l.size = info.Size()
	l.lineStart = l.size == 0
	if l.size > 0 {
		var last [1]byte
		if _, err := file.ReadAt(last[:], l.size-1); err != nil {
			l.file = nil
			return errors.Join(err, file.Close())
		}
		l.lineStart = last[0] == '\n'
	}
	return nil
}

func (l *openVMMRotatingSerialLog) Write(data []byte) (int, error) {
	if l.failure != nil {
		return 0, l.failure
	}
	written := 0
	for len(data) > 0 {
		// Reserve one read buffer at line boundaries so ordinary console lines
		// (including fragmented completion markers) stay in one tail generation.
		headroom := min(l.maxSize, int64(openVMMSerialBufferSize))
		if l.size >= l.maxSize || (l.lineStart && l.size > 0 && l.maxSize-l.size < headroom) {
			lineStart := l.lineStart
			if err := l.rotate(); err != nil {
				l.failure = err
				return written, err
			}
			l.lineStart = lineStart
		}
		size := min(int64(len(data)), l.maxSize-l.size, int64(openVMMSerialBufferSize))
		if l.maxSize-l.size <= 2*headroom {
			if newline := bytes.LastIndexByte(data[:size], '\n'); newline >= 0 {
				size = int64(newline + 1)
			}
		}
		n, err := l.file.Write(data[:size])
		if n > 0 {
			l.lineStart = data[n-1] == '\n'
		}
		l.size += int64(n)
		written += n
		data = data[n:]
		if err != nil {
			l.failure = err
			return written, err
		}
		if int64(n) != size {
			l.failure = io.ErrShortWrite
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (l *openVMMRotatingSerialLog) rotate() error {
	if err := l.file.Sync(); err != nil {
		return err
	}
	if err := l.Close(); err != nil {
		return err
	}
	oldest := l.path + "." + strconv.Itoa(openVMMSerialLogBackups-1)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove oldest serial log: %w", err)
	}
	for index := openVMMSerialLogBackups - 2; index >= -1; index-- {
		source := l.path
		if index >= 0 {
			source += "." + strconv.Itoa(index)
		}
		if err := os.Rename(source, l.path+"."+strconv.Itoa(index+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rotate serial log: %w", err)
		}
	}
	return l.open()
}

func (l *openVMMRotatingSerialLog) Sync() error {
	if l.file == nil {
		return nil
	}
	return l.file.Sync()
}

func (l *openVMMRotatingSerialLog) Close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
