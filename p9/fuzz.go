// +build gofuzz

package p9

import (
	"bytes"

	"github.com/u-root/u-root/pkg/ulog"
)

func Fuzz(data []byte) int {
	buf := bytes.NewBuffer(data)
	tag, msg, err := recv(ulog.Null, buf, DefaultMessageSize, msgRegistry.get)
	if err != nil {
		if msg != nil {
			panic("msg !=nil on error")
		}
		panic(err)
	}
	buf.Reset()
	send(ulog.Null, buf, tag, msg)
	if err != nil {
		panic(err)
	}
	return 1
}
