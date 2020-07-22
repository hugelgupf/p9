// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package p9

import (
	"net"
	"reflect"
	"testing"

	"github.com/u-root/u-root/pkg/ulog/ulogtest"
)

const (
	msgTypeBadEncode = iota + 252
	msgTypeBadDecode
	msgTypeUnregistered
)

// badDecode overruns on decode.
type badDecode struct{}

func (*badDecode) decode(b *buffer) { b.markOverrun() }
func (*badDecode) encode(b *buffer) {}
func (*badDecode) typ() msgType     { return msgTypeBadDecode }
func (*badDecode) String() string   { return "badDecode{}" }

// unregistered is not registered on decode.
type unregistered struct{}

func (*unregistered) decode(b *buffer) {}
func (*unregistered) encode(b *buffer) {}
func (*unregistered) typ() msgType     { return msgTypeUnregistered }
func (*unregistered) String() string   { return "unregistered{}" }

func DISABLEDTestRecvClosed(t *testing.T) {
	server, client := net.Pipe()

	defer server.Close()
	client.Close()

	l := ulogtest.Logger{TB: t}
	_, _, err := recv(l, server, maximumLength, msgRegistry.get)
	if err == nil {
		t.Fatalf("got err nil expected non-nil")
	}
	if _, ok := err.(ConnError); !ok {
		t.Fatalf("got err %v expected ErrSocket", err)
	}
}

func DISABLEDTestSendClosed(t *testing.T) {
	server, client := net.Pipe()

	server.Close()
	defer client.Close()

	l := ulogtest.Logger{TB: t}
	err := send(l, client, tag(1), &tlopen{})
	if err == nil {
		t.Fatalf("send got err nil expected non-nil")
	}
	if _, ok := err.(ConnError); !ok {
		t.Fatalf("got err %v expected ErrSocket", err)
	}
}

func init() {
	msgRegistry.register(msgTypeBadDecode, func() message { return &badDecode{} })
}

func TestSendAndRecv(t *testing.T) {
	type args struct {
		tag tag
		m   message
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Valid",
			args: args{
				tag(1), &tlopen{},
			},
			wantErr: false,
		},
		{
			name: "InvalidType",
			args: args{
				tag(1), &unregistered{},
			},
			wantErr: true,
		},
		{
			name: "RecvOverrun",
			args: args{
				tag(1), &badDecode{},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()

			l := ulogtest.Logger{TB: t}
			fin := make(chan struct{})
			go func() {
				defer func() {
					fin <- struct{}{}
				}()
				tagg, m, err := recv(l, server, maximumLength, msgRegistry.get)
				if (err != nil) != tt.wantErr {
					t.Errorf("recv() error = %v, wantRecvErr %v", err, tt.wantErr)
					return
				}
				if !tt.wantErr && tagg != tag(1) {
					t.Fatalf("got tag %v expected 1", tagg)
				}
				expectedType := reflect.TypeOf(tt.args.m)
				recievedType := reflect.TypeOf(m)
				if !tt.wantErr && expectedType != recievedType {
					t.Fatalf("got message %v expected %v", recievedType, expectedType)
				}

			}()
			if err := send(l, client, tag(1), tt.args.m); err != nil {
				t.Fatalf("got err nil expected non-nil")
			}
			<-fin
		})
	}
}
