package sshtcelltty

import (
	"fmt"
	"io"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/gliderlabs/ssh"
)

type InterleavedSSHSession interface {
	ssh.Session
	Interleave(byte)
}

type interleavedSSHSession struct {
	ssh.Session
	interleaved chan byte
	incoming    chan readResult
	stop        chan bool
}

func (i *interleavedSSHSession) loop() {
	defer close(i.incoming)
	for {
		buf := make([]byte, 1)
		_, err := i.Session.Read(buf)
		select {
		case i.incoming <- readResult{b: buf[0], err: err}:
		case <-i.stop:
			return
		}
	}
}

func (i *interleavedSSHSession) Read(b []byte) (int, error) {
	select {
	case rr, ok := <-i.incoming:
		if !ok {
			return 0, io.EOF
		}
		if len(b) > 0 {
			b[0] = rr.b
			return 1, rr.err
		}
		return 0, rr.err
	case i := <-i.interleaved:
		if len(b) > 0 {
			b[0] = i
		}
		return 1, nil
	}
}

func (i *interleavedSSHSession) Interleave(b byte) {
	i.interleaved <- b
}

func (i *interleavedSSHSession) Close() error {
	close(i.stop)
	return i.Session.Close()
}

func NewInterleavedSSHSession(sess ssh.Session) InterleavedSSHSession {
	res := &interleavedSSHSession{
		Session:     sess,
		interleaved: make(chan byte),
		incoming:    make(chan readResult),
		stop:        make(chan bool),
	}
	go res.loop()
	return res
}

type readResult struct {
	b   byte
	err error
}

type SSHTTY struct {
	Sess InterleavedSSHSession

	resizeCallback func()
	stop           chan bool
	drain          chan bool
	mutex          sync.Mutex
	width          int
	height         int
}

func (s *SSHTTY) readOneByte(inc chan readResult) {
	defer close(inc)
	buf := make([]byte, 1)
	_, err := s.Sess.Read(buf)
	select {
	case <-s.stop:
		s.Sess.Interleave(buf[0])
		return
	case inc <- readResult{b: buf[0], err: err}:
	}
}

func (s *SSHTTY) Read(b []byte) (int, error) {
	inc := make(chan readResult)
	go s.readOneByte(inc)
	select {
	case v, ok := <-inc:
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

func (s *SSHTTY) WindowSize() (tcell.WindowSize, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return tcell.WindowSize{
		Width:       s.width,
		Height:      s.height,
		PixelWidgh:  s.width,
		PixelHeight: s.height,
	}, nil
}

func (s *SSHTTY) NotifyResize(cb func()) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.resizeCallback = cb
}
