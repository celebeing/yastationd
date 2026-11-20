package app

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

type followTestHandler struct {
	*satellite
	connected chan *wyoming.Conn
}

func (h *followTestHandler) OnConnect(c *wyoming.Conn) { h.connected <- c }

func TestFollowListenDeadline(t *testing.T) {
	for _, tc := range []struct {
		name, src    string
		seconds      int
		speech, stop bool
	}{
		{"follow timeout", srcFollow, 1, false, true},
		{"follow speech", srcFollow, 1, true, false},
		{"speech at deadline", srcFollow, 1, true, false},
		{"old timer", srcFollow, 1, false, false},
		{"disabled", srcFollow, 0, false, false},
		{"wake", srcWake, 1, false, false},
		{"key", srcKey, 1, false, false},
		{"intercom", srcFollow, 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Parse(strings.NewReader(fmt.Sprintf("follow_up_listen_timeout = %d\n", tc.seconds)))
			if err != nil {
				t.Fatal(err)
			}
			a := newExpressApp(t)
			a.conf.Store(&cfg)
			a.mic = &hw.Mic{Log: a.log}
			resume, err := a.mic.Suspend()
			if err != nil {
				t.Fatal(err)
			}
			defer resume()
			s := &satellite{a: a}
			h := &followTestHandler{satellite: s, connected: make(chan *wyoming.Conn, 1)}
			server := &wyoming.Server{Handler: h, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := ln.Addr().String()
			ln.Close()
			go server.Serve(addr)
			defer server.Close()
			var peer net.Conn
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
				peer, err = net.Dial("tcp", addr)
				if err == nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			c := <-h.connected
			s.OnEvent(c, wyoming.Event{Type: wyoming.TypeRunSatellite})
			events := make(chan string, 8)
			go func() {
				r := bufio.NewReader(peer)
				for {
					ev, err := wyoming.Read(r)
					if err != nil {
						close(events)
						return
					}
					events <- ev.Type
				}
			}()
			if tc.name == "intercom" {
				s.startListeningWith(srcFollow, nil, false)
			} else {
				s.startListening(tc.src)
			}
			s.OnEvent(c, wyoming.Event{Type: wyoming.TypeTranscribe})
			if tc.name == "old timer" {
				s.mu.Lock()
				old := s.listen
				s.mu.Unlock()
				s.stopListening(old, "test superseded")
				s.startListening(srcWake)
			}
			if tc.speech {
				if tc.name == "speech at deadline" {
					time.Sleep(900 * time.Millisecond)
				}
				s.OnEvent(c, wyoming.Event{Type: wyoming.TypeVoiceStarted})
			}
			timer := time.NewTimer(1300 * time.Millisecond)
			defer timer.Stop()
			stopped := false
		loop:
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						stopped = true
						break loop
					}
					if ev == wyoming.TypeAudioStop && tc.name != "old timer" {
						stopped = true
						break loop
					}
				case <-timer.C:
					break loop
				}
			}
			if stopped != tc.stop {
				t.Errorf("audio-stop = %v, want %v", stopped, tc.stop)
			}
			if tc.stop && (a.ringStack.Has("voice") || a.matrixStack.Has("voice")) {
				t.Error("listening indication remained after timeout")
			}
			if tc.stop {
				for _, kind := range []string{wyoming.TypeTranscribe, wyoming.TypeVoiceStarted, wyoming.TypeTranscript, wyoming.TypeSynthesize, wyoming.TypeError} {
					s.OnEvent(c, wyoming.Event{Type: kind, Data: map[string]any{"text": "question?"}})
					if a.ringStack.Has("voice") || a.matrixStack.Has("voice") || s.awaitingReply() {
						t.Errorf("late %s changed the dialog or indication", kind)
					}
				}
			}
			if tc.name == "old timer" && !a.ringStack.Has("voice") {
				t.Error("old timer cleared the new session's indication")
			}
			if tc.name == "follow timeout" {
				deadline := time.Now().Add(time.Second)
				for {
					s.mu.Lock()
					listening := s.listen != nil
					s.mu.Unlock()
					if !listening {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("timed-out session did not finish")
					}
					time.Sleep(time.Millisecond)
				}
				next, err := net.Dial("tcp", addr)
				if err != nil {
					t.Fatal(err)
				}
				defer next.Close()
				nextConn := <-h.connected
				s.OnEvent(nextConn, wyoming.Event{Type: wyoming.TypeRunSatellite})
				s.startListening(srcWake)
				a.unexpress("voice")
				s.OnEvent(c, wyoming.Event{Type: wyoming.TypeTranscribe})
				if a.ringStack.Has("voice") || a.matrixStack.Has("voice") {
					t.Error("old connection changed the new session's indication")
				}
				s.OnEvent(nextConn, wyoming.Event{Type: wyoming.TypeTranscribe})
				if !a.ringStack.Has("voice") {
					t.Error("new connection did not update its indication")
				}
				next.SetReadDeadline(time.Now().Add(time.Second))
				r := bufio.NewReader(next)
				first, err := wyoming.Read(r)
				if err != nil || first.Type != wyoming.TypeDetection {
					t.Fatalf("wake after reconnect: %v, %v", first.Type, err)
				}
				run, err := wyoming.Read(r)
				if err != nil || run.Type != wyoming.TypeRunPipeline {
					t.Fatalf("pipeline after reconnect: %v, %v", run.Type, err)
				}
				s.mu.Lock()
				wake := s.listen
				s.mu.Unlock()
				s.stopListening(wake, "test second follow-up")
				s.startListening(srcFollow)
				next.SetReadDeadline(time.Now().Add(2 * time.Second))
				for {
					_, err := wyoming.Read(r)
					if err != nil {
						if err != io.EOF {
							t.Fatalf("second connection did not close: %v", err)
						}
						break
					}
				}
				for _, kind := range []string{wyoming.TypeTranscribe, wyoming.TypeTranscript, wyoming.TypeSynthesize, wyoming.TypeError} {
					s.OnEvent(c, wyoming.Event{Type: kind, Data: map[string]any{"text": "question?"}})
					if a.ringStack.Has("voice") || a.matrixStack.Has("voice") || s.awaitingReply() {
						t.Errorf("event %s from first cancelled connection changed state after second timeout", kind)
					}
				}
			}
			s.mu.Lock()
			v := s.listen
			s.mu.Unlock()
			if v != nil {
				s.stopListening(v, "test cleanup")
			}
		})
	}
}
