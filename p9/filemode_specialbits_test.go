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
	"os"
	"testing"
)

// The setuid, setgid, and sticky bits (04000/02000/01000) are part of a file's
// permission word in 9P2000.L (the mode field of Tlcreate/Tmkdir and the
// permissions field of Tsetattr all carry st_mode's low 12 bits). They must
// survive FileMode.Permissions, the wire encode/decode, Attr.Apply, and the
// os.FileMode conversions, or a guest can never create a setuid binary, a
// setgid directory, or a sticky directory over 9P.

func TestPermissionsKeepsSpecialBits(t *testing.T) {
	for _, perm := range []FileMode{0o4755, 0o2755, 0o1777, 0o7755} {
		if got := perm.Permissions(); got != perm {
			t.Errorf("FileMode(%#o).Permissions() = %#o, want %#o", uint32(perm), uint32(got), uint32(perm))
		}
	}
}

func TestModeFromOSPreservesSpecialBits(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   os.FileMode
		want FileMode
	}{
		{"setuid", 0o755 | os.ModeSetuid, ModeRegular | 0o4755},
		{"setgid", 0o755 | os.ModeSetgid, ModeRegular | 0o2755},
		{"sticky-dir", 0o777 | os.ModeDir | os.ModeSticky, ModeDirectory | 0o1777},
		{"setuid+setgid", 0o755 | os.ModeSetuid | os.ModeSetgid, ModeRegular | 0o6755},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ModeFromOS(tc.in); got != tc.want {
				t.Errorf("ModeFromOS(%v) = %#o, want %#o", tc.in, uint32(got), uint32(tc.want))
			}
		})
	}
}

func TestOSModePreservesSpecialBits(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   FileMode
		want os.FileMode
	}{
		{"setuid", ModeRegular | 0o4755, 0o755 | os.ModeSetuid},
		{"setgid", ModeRegular | 0o2755, 0o755 | os.ModeSetgid},
		{"sticky-dir", ModeDirectory | 0o1777, 0o777 | os.ModeDir | os.ModeSticky},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.OSMode(); got != tc.want {
				t.Errorf("FileMode(%#o).OSMode() = %v, want %v", uint32(tc.in), got, tc.want)
			}
		})
	}
}

func TestModeOSRoundTripSpecialBits(t *testing.T) {
	for _, in := range []os.FileMode{
		0o755 | os.ModeSetuid,
		0o750 | os.ModeDir | os.ModeSetgid,
		0o777 | os.ModeDir | os.ModeSticky,
	} {
		if got := ModeFromOS(in).OSMode(); got != in {
			t.Errorf("ModeFromOS(%v).OSMode() = %v, want round-trip", in, got)
		}
	}
}

// TestSetAttrPermissionsWireRoundTrip exercises the Tsetattr (chmod) path: a
// SetAttr carrying setuid/setgid/sticky must survive encode→decode. This is the
// bit a guest's chmod(2) rides on.
func TestSetAttrPermissionsWireRoundTrip(t *testing.T) {
	for _, perm := range []FileMode{0o4755, 0o2755, 0o1777, 0o6755, 0o7755} {
		in := SetAttr{Permissions: perm}
		var enc buffer
		in.encode(&enc)
		dec := buffer{data: enc.data}
		var out SetAttr
		out.decode(&dec)
		if dec.overflow {
			t.Fatalf("perm %#o: buffer overflow on decode", uint32(perm))
		}
		if out.Permissions != perm {
			t.Errorf("SetAttr.Permissions wire round-trip: %#o -> %#o (special bits dropped)", uint32(perm), uint32(out.Permissions))
		}
	}
}

// TestCreatePermissionsWireRoundTrip exercises the Tlcreate path: creating a
// file with setuid/setgid set in the mode must survive encode→decode.
func TestCreatePermissionsWireRoundTrip(t *testing.T) {
	for _, perm := range []FileMode{0o4755, 0o2755, 0o1777, 0o6755} {
		in := tlcreate{Name: "x", Permissions: perm}
		var enc buffer
		in.encode(&enc)
		dec := buffer{data: enc.data}
		var out tlcreate
		out.decode(&dec)
		if out.Permissions != perm {
			t.Errorf("tlcreate.Permissions wire round-trip: %#o -> %#o (special bits dropped)", uint32(perm), uint32(out.Permissions))
		}
	}
}
