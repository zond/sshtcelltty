package sshtcelltty

import (
	"fmt"
	"io"
	"sync"

	"github.com/gliderlabs/ssh"
)

type SSHTTY struct {
	Sess ssh.Session

	resizeCallback func()
	stop           chan bool
	drain          chan bool
	mutex          sync.Mutex
	width          int
	height         int
}

type readResult struct {
	b   byte
	err error
}

func (s *SSHTTY) readOneByte(c chan readResult) {
	defer close(c)
	buf := make([]byte, 1)
	_, err := s.Sess.Read(buf)
	select {
	case <-s.stop:
		return
	case c <- readResult{b: buf[0], err: err}:
	}
}

func (s *SSHTTY) Read(b []byte) (int, error) {
	incoming := make(chan readResult)
	go s.readOneByte(incoming)
	select {
	case v, ok := <-incoming:
		if ok {
			b[0] = v.b
			return 1, v.err
		} else {
			return 0, io.EOF
		}
	case <-s.drain:
		return 0, nil
	}
}

func (s *SSHTTY) Write(b []byte) (int, error) {
	return s.Sess.Write(b)
}

func (s *SSHTTY) Close() error {
	return nil
}

func (s *SSHTTY) Start() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	pty, winCh, isPTY := s.Sess.Pty()
	if !isPTY {
		return fmt.Errorf("session is not interactive")
	}

	s.width, s.height = pty.Window.Width, pty.Window.Height

	s.stop = make(chan bool)
	go func() {
		for {
			select {
			case ev := <-winCh:
				cb := func() func() {
					s.mutex.Lock()
					defer s.mutex.Unlock()
					s.width = ev.Width
					s.height = ev.Height
					return s.resizeCallback
				}()
				if cb != nil {
					cb()
				}
			case <-s.stop:
				return
			}
		}
	}()

	s.drain = make(chan bool)

	return nil
}

func (s *SSHTTY) Drain() error {
	close(s.drain)
	return nil
}

func (s *SSHTTY) Stop() error {
	close(s.stop)
	return nil
}

func (s *SSHTTY) WindowSize() (int, int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.width, s.height, nil
}

func (s *SSHTTY) NotifyResize(cb func()) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.resizeCallback = cb
}
