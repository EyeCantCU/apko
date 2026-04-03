// Copyright 2025 Chainguard, Inc.
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

package build

import (
	"fmt"
	"slices"
	"testing"
)

func TestAlignStacks(t *testing.T) {
	usr := []*file{{
		path: "usr",
	}, {
		path: "usr/lib",
	}}
	etc := []*file{{
		path: "etc",
	}, {
		path: "etc/apk",
	}, {
		path: "etc/apk/key",
	}}
	for i, tc := range []struct {
		stack  []*file
		before []*file
		diff   []*file
		after  []*file
	}{{
		stack:  usr,
		before: usr,
		after:  usr,
	}, {
		stack: usr,
		after: usr,
		diff:  usr,
	}, {
		stack:  usr,
		before: etc,
		after:  usr,
		diff:   usr,
	}, {
		stack:  etc,
		before: usr,
		after:  etc,
		diff:   etc,
	}, {
		stack:  etc[:2],
		before: etc,
		after:  etc[:2],
	}, {
		stack:  etc,
		before: etc[:2],
		after:  etc,
		diff:   etc[2:],
	}} {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			// clone to avoid mutating the usr and etc slices directly
			w := &layerWriter{stack: slices.Clone(tc.before)}

			if err := compareStacks(w.alignStacks(tc.stack), tc.diff); err != nil {
				t.Errorf("alignStacks() mismatch: %v", err)
			}
			if err := compareStacks(w.stack, tc.after); err != nil {
				t.Errorf("w.stack mismatch: %v", err)
			}
		})
	}
}

// NB: this only cares about path
func compareStacks(a, b []*file) error {
	if len(a) != len(b) {
		return fmt.Errorf("len(a) = %d; len(b) = %d", len(a), len(b))
	}

	for i := range len(a) {
		if a[i].path != b[i].path {
			return fmt.Errorf("a[%d] = %s; b[%d] = %s", i, a[i].path, i, b[i].path)
		}
	}

	return nil
}
