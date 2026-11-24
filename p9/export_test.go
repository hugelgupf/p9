// Copyright 2026 The gVisor Authors.
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

// SendXattrWalk sends a Txattrwalk for f, a File returned by a Client, and
// returns the new xattr fid. Client does not otherwise expose it.
func SendXattrWalk(f File, name string) (File, error) {
	c := f.(*clientFile)
	id, ok := c.client.fidPool.Get()
	if !ok {
		return nil, ErrOutOfFIDs
	}
	if err := c.client.sendRecv(&txattrwalk{fid: c.fid, newFID: fid(id), Name: name}, &rxattrwalk{}); err != nil {
		c.client.fidPool.Put(id)
		return nil, err
	}
	return c.client.newFile(fid(id)), nil
}
