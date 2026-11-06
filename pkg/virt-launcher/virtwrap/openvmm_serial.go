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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"
)

const (
	openVMMSerialSocket         = "openvmm-serial0"
	openVMMSerialConnectWarning = 5 * time.Second
	openVMMSerialDrainTimeout   = 2 * time.Second
	openVMMSerialWriteTimeout   = time.Second
	openVMMSerialBufferSize     = 32 * 1024
	openVMMSerialQueueBytes     = 512 * 1024
	openVMMSerialRetryMinDelay  = 10 * time.Millisecond
	openVMMSerialRetryMaxDelay  = time.Second
	openVMMSerialRetryResetTime = 5 * time.Second
)

type openVMMSerialLog interface {
	io.WriteCloser
	Sync() error
}

type openVMMSerialClient struct {
	conn    net.Conn
	tx      chan struct{}
	pending bytes.Buffer
	done    chan struct{}
	once    sync.Once
}

func (c *openVMMSerialClient) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

type openVMMSerialBroker struct {
	mutex    sync.Mutex
	input    sync.Mutex
	listener net.Listener
	upstream net.Conn
	client   *openVMMSerialClient
	stopping bool
	stopOnce sync.Once
	workers  sync.WaitGroup
	cancel   context.CancelFunc
	done     chan struct{}
	log      openVMMSerialLog
	report   func(error)
	recovery func()
}

// Dialing starts before the VM starts, but does not prove that OpenVMM has
// accepted the connection. Output before acceptance can still be lost.
func newOpenVMMSerialBroker(directory string, capture bool, setOwnership func(string) error, report func(error), recovery func()) (*openVMMSerialBroker, error) {
	b := &openVMMSerialBroker{done: make(chan struct{}), report: report, recovery: recovery}
	if capture {
		writer, err := newOpenVMMSerialLog(filepath.Join(directory, "virt-serial0-log"), setOwnership)
		if err != nil {
			return nil, fmt.Errorf("prepare OpenVMM serial log: %w", err)
		}
		b.log = writer
	}
	listener, err := net.Listen("unix", filepath.Join(directory, "virt-serial0"))
	if err != nil {
		if b.log != nil {
			if closeErr := b.log.Close(); closeErr != nil {
				report(fmt.Errorf("close serial log after listener failure: %w", closeErr))
			}
		}
		return nil, fmt.Errorf("listen on OpenVMM console socket: %w", err)
	}
	b.listener = listener
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	go b.run(ctx, filepath.Join(directory, openVMMSerialSocket))
	return b, nil
}

func (b *openVMMSerialBroker) run(ctx context.Context, address string) {
	defer close(b.done)
	defer b.finish()
	connectedBefore := false
	retryDelay := openVMMSerialRetryMinDelay
	for {
		conn, err := b.connect(ctx, address, connectedBefore, &retryDelay)
		if err != nil {
			return
		}
		b.mutex.Lock()
		if b.stopping {
			b.mutex.Unlock()
			_ = conn.Close()
			return
		}
		b.upstream = conn
		if !connectedBefore {
			b.workers.Add(1)
		}
		b.mutex.Unlock()
		if !connectedBefore {
			go b.acceptClients()
		} else if b.recovery != nil {
			b.recovery()
		}
		connectedBefore = true
		connectedAt := time.Now()
		readErr := b.readGuest(conn)

		b.mutex.Lock()
		stopping := b.stopping
		b.upstream = nil
		client := b.client
		b.client = nil
		b.mutex.Unlock()
		if client != nil {
			client.close()
		}
		_ = conn.Close()
		if stopping {
			if !errors.Is(readErr, io.EOF) {
				b.report(fmt.Errorf("OpenVMM serial final drain incomplete: %w", readErr))
			}
			return
		}
		b.report(fmt.Errorf("OpenVMM serial transport lost; reconnecting, but guest output during the gap cannot be recovered: %w", readErr))
		// Keep increasing the delay across immediately flapping connections.
		// Only a sustained connection resets the recovery retry budget.
		if time.Since(connectedAt) >= openVMMSerialRetryResetTime {
			retryDelay = openVMMSerialRetryMinDelay
		}
		if !waitOpenVMMSerialRetry(ctx, retryDelay) {
			return
		}
		retryDelay = nextOpenVMMSerialRetryDelay(retryDelay)
	}
}

func (b *openVMMSerialBroker) connect(ctx context.Context, address string, recovering bool, retryDelay *time.Duration) (net.Conn, error) {
	warnAt := time.Now().Add(openVMMSerialConnectWarning)
	for {
		conn, err := (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext(ctx, "unix", address)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !warnAt.IsZero() && time.Now().After(warnAt) {
			b.report(fmt.Errorf("OpenVMM serial connection unavailable after %s; still retrying: %w", openVMMSerialConnectWarning, err))
			warnAt = time.Time{}
		}
		delay := openVMMSerialRetryMinDelay
		if recovering {
			delay = *retryDelay
			*retryDelay = nextOpenVMMSerialRetryDelay(delay)
		}
		if !waitOpenVMMSerialRetry(ctx, delay) {
			return nil, ctx.Err()
		}
	}
}

func (b *openVMMSerialBroker) readGuest(conn net.Conn) error {
	buffer := make([]byte, openVMMSerialBufferSize)
	for {
		n, readErr := conn.Read(buffer)
		if n > 0 {
			if b.log != nil {
				written, writeErr := b.log.Write(buffer[:n])
				if writeErr == nil && written != n {
					writeErr = io.ErrShortWrite
				}
				if writeErr != nil {
					b.report(fmt.Errorf("OpenVMM serial capture disabled after log failure; subsequent output is not persisted: %w", writeErr))
					if closeErr := b.log.Close(); closeErr != nil {
						b.report(fmt.Errorf("close failed OpenVMM serial log: %w", closeErr))
					}
					b.log = nil
				}
			}
			b.deliver(buffer[:n])
		}
		if readErr != nil {
			return readErr
		}
	}
}

func nextOpenVMMSerialRetryDelay(delay time.Duration) time.Duration {
	return min(2*delay, openVMMSerialRetryMaxDelay)
}

func waitOpenVMMSerialRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (b *openVMMSerialBroker) acceptClients() {
	defer b.workers.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				b.report(fmt.Errorf("accept OpenVMM console connection: %w", err))
			}
			return
		}
		client := &openVMMSerialClient{conn: conn, tx: make(chan struct{}, 1), done: make(chan struct{})}
		b.mutex.Lock()
		stopping := b.stopping
		if stopping || b.upstream == nil {
			b.mutex.Unlock()
			client.close()
			if stopping {
				return
			}
			continue
		}
		if b.client != nil {
			b.client.close()
		}
		b.client = client
		b.workers.Add(2)
		b.mutex.Unlock()
		go b.readClient(client)
		go b.writeClient(client)
	}
}

func (b *openVMMSerialBroker) detach(client *openVMMSerialClient) {
	b.mutex.Lock()
	if b.client == client {
		b.client = nil
	}
	b.mutex.Unlock()
	client.close()
}

func (b *openVMMSerialBroker) deliver(data []byte) {
	b.mutex.Lock()
	client := b.client
	if client == nil {
		b.mutex.Unlock()
		return
	}
	if len(data) <= openVMMSerialQueueBytes-client.pending.Len() {
		client.pending.Write(data)
		select {
		case client.tx <- struct{}{}:
		default:
		}
		b.mutex.Unlock()
		return
	}
	b.client = nil
	b.mutex.Unlock()
	client.close()
	b.report(fmt.Errorf("disconnected slow OpenVMM console: %d-byte output queue full; serial capture continues", openVMMSerialQueueBytes))
}

func (b *openVMMSerialBroker) readClient(client *openVMMSerialClient) {
	defer b.workers.Done()
	defer b.detach(client)
	buffer := make([]byte, 4096)
	for {
		n, err := client.conn.Read(buffer)
		if n > 0 {
			b.input.Lock()
			b.mutex.Lock()
			active := b.client == client && !b.stopping
			upstream := b.upstream
			b.mutex.Unlock()
			if !active {
				b.input.Unlock()
				return
			}
			writeErr := writeOpenVMMSerial(upstream, buffer[:n])
			b.input.Unlock()
			if writeErr != nil {
				b.report(fmt.Errorf("forward OpenVMM console input: %w", writeErr))
				var timeout net.Error
				if !errors.As(writeErr, &timeout) || !timeout.Timeout() {
					_ = upstream.Close()
				}
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				b.report(fmt.Errorf("read OpenVMM console input: %w", err))
			}
			return
		}
	}
}

func (b *openVMMSerialBroker) writeClient(client *openVMMSerialClient) {
	defer b.workers.Done()
	defer b.detach(client)
	buffer := make([]byte, openVMMSerialBufferSize)
	for {
		select {
		case <-client.done:
			return
		case <-client.tx:
			for {
				b.mutex.Lock()
				n := copy(buffer, client.pending.Next(len(buffer)))
				b.mutex.Unlock()
				if n == 0 {
					break
				}
				if err := writeOpenVMMSerial(client.conn, buffer[:n]); err != nil {
					if !errors.Is(err, net.ErrClosed) {
						b.report(fmt.Errorf("disconnect OpenVMM console after output failure; serial capture continues: %w", err))
					}
					return
				}
			}
		}
	}
}

func writeOpenVMMSerial(conn net.Conn, data []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(openVMMSerialWriteTimeout)); err != nil {
		return err
	}
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// Stop bounds the upstream drain, not filesystem syscall latency. The reader
// owns the log until it has consumed EOF or reached the absolute drain deadline.
func (b *openVMMSerialBroker) Stop() {
	select {
	case <-b.done:
		return
	default:
	}
	b.stopOnce.Do(func() {
		b.mutex.Lock()
		b.stopping = true
		upstream := b.upstream
		client := b.client
		b.client = nil
		b.mutex.Unlock()
		b.cancel()
		_ = b.listener.Close()
		if client != nil {
			client.close()
		}
		if upstream != nil {
			if err := upstream.SetReadDeadline(time.Now().Add(openVMMSerialDrainTimeout)); err != nil && !errors.Is(err, net.ErrClosed) {
				b.report(fmt.Errorf("set OpenVMM serial final drain deadline: %w", err))
				_ = upstream.Close()
			}
		}
	})
	<-b.done
}

func (b *openVMMSerialBroker) finish() {
	b.mutex.Lock()
	b.stopping = true
	upstream := b.upstream
	client := b.client
	b.client = nil
	b.mutex.Unlock()
	b.cancel()
	_ = b.listener.Close()
	if client != nil {
		client.close()
	}
	if upstream != nil {
		_ = upstream.Close()
	}
	b.workers.Wait()
	if b.log != nil {
		if err := b.log.Sync(); err != nil {
			b.report(fmt.Errorf("sync OpenVMM serial log: %w", err))
		}
		if err := b.log.Close(); err != nil {
			b.report(fmt.Errorf("close OpenVMM serial log: %w", err))
		}
	}
}
