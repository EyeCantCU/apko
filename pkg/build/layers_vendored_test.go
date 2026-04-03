// Copyright 2026 Chainguard, Inc.
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
	"archive/tar"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"chainguard.dev/apko/pkg/apk/apk"
	apkfs "chainguard.dev/apko/pkg/apk/fs"
)

func TestGroupByVendored(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	writeDistInfo(t, fsys, sp, "big_package-1.0.dist-info", []string{
		"big_package/data.so,sha256=abc,10000",
	})
	writeDistInfo(t, fsys, sp, "small_package-1.0.dist-info", []string{
		"small_package/__init__.py,sha256=abc,100",
	})

	groups, err := groupByVendored(fsys, 1, "balanced")
	if err != nil {
		t.Fatalf("groupByVendored: %v", err)
	}

	if len(groups) != 1 {
		t.Errorf("got %d groups, want 1", len(groups))
	}

	for _, g := range groups {
		if len(g.paths) == 0 {
			t.Error("expected all groups to have paths")
		}
		if len(g.pkgs) != 0 {
			t.Error("vendored groups should not contain APK packages")
		}
	}
}

func TestGroupByVendoredStability(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	for _, name := range []string{"alpha", "beta", "gamma"} {
		writeDistInfo(t, fsys, sp, name+"-1.0.dist-info", []string{
			fmt.Sprintf("%s/data.bin,sha256=abc,1000", name),
		})
	}

	var firstResult []string
	for i := range 10 {
		groups, err := groupByVendored(fsys, 2, "stable")
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}

		var desc []string
		for _, g := range groups {
			var parts []string
			for _, p := range g.paths {
				parts = append(parts, "prefix:"+p)
			}
			desc = append(desc, fmt.Sprintf("{%s size=%d}", strings.Join(parts, ","), g.size))
		}

		if i == 0 {
			firstResult = desc
		} else if !slices.Equal(desc, firstResult) {
			t.Errorf("run %d: result differs from run 0\ngot:  %v\nwant: %v", i, desc, firstResult)
		}
	}
}

func TestGroupByVendoredBudgetOverflow(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	for i := range 20 {
		name := fmt.Sprintf("pkg_%02d", i)
		writeDistInfo(t, fsys, sp, name+"-1.0.dist-info", []string{
			fmt.Sprintf("%s/__init__.py,sha256=abc,%d", name, (20-i)*100),
		})
	}

	groups, err := groupByVendored(fsys, 3, "balanced")
	if err != nil {
		t.Fatalf("groupByVendored: %v", err)
	}

	if len(groups) > 3 {
		t.Errorf("got %d groups, want <= 3", len(groups))
	}
}

func TestHashGroupsStability(t *testing.T) {
	makeGroups := func(extra bool) []*group {
		groups := []*group{
			{paths: []string{"torch"}, tiebreaker: "torch"},
			{paths: []string{"numpy"}, tiebreaker: "numpy"},
			{paths: []string{"vllm"}, tiebreaker: "vllm"},
			{paths: []string{"keras"}, tiebreaker: "keras"},
		}
		if extra {
			groups = append(groups, &group{
				paths: []string{"click"}, tiebreaker: "click",
			})
		}
		return groups
	}

	without, err := assignGroups(makeGroups(false), 3, "stable")
	if err != nil {
		t.Fatal(err)
	}
	with, err := assignGroups(makeGroups(true), 3, "stable")
	if err != nil {
		t.Fatal(err)
	}

	groupOf := func(groups []*group) map[string]string {
		m := map[string]string{}
		for _, g := range groups {
			for _, p := range g.paths {
				m[p] = g.tiebreaker
			}
		}
		return m
	}

	gWithout := groupOf(without)
	gWith := groupOf(with)

	for _, pkg := range []string{"torch", "numpy", "vllm", "keras"} {
		if gWithout[pkg] != gWith[pkg] {
			t.Errorf("package %q moved from group %q to %q when unrelated package added",
				pkg, gWithout[pkg], gWith[pkg])
		}
	}
}

func TestGroupByVendoredNoPython(t *testing.T) {
	fsys := apkfs.NewMemFS()
	if err := fsys.MkdirAll("usr/bin", 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := groupByVendored(fsys, 5, "balanced")
	if err == nil {
		t.Fatal("expected error when no vendored packages exist")
	}
}

func TestSplitLayersWithVendoredGroups(t *testing.T) {
	ctx := t.Context()
	fsys := apkfs.NewMemFS()

	dirs := []string{
		"usr/lib/apk/db",
		"usr/lib/python3.12/site-packages/numpy",
		"usr/lib/python3.12/site-packages/tensorflow",
		"usr/bin",
	}
	for _, d := range dirs {
		if err := fsys.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := fsys.WriteFile("usr/lib/apk/db/installed", []byte("idb content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile("usr/lib/python3.12/site-packages/numpy/__init__.py", []byte("numpy init"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile("usr/lib/python3.12/site-packages/tensorflow/__init__.py", []byte("tf init"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile("usr/bin/python3", []byte("python binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	pkg1 := &apk.Package{Name: "py3-ml", Origin: "py3-ml", Version: "1.0.0", InstalledSize: 5000}

	numpyGroup := &group{
		paths:      []string{"usr/lib/python3.12/site-packages/numpy"},
		pkgs:       []*apk.Package{pkg1},
		size:       1000,
		tiebreaker: "numpy",
	}
	tfGroup := &group{
		paths:      []string{"usr/lib/python3.12/site-packages/tensorflow"},
		pkgs:       []*apk.Package{pkg1},
		size:       2000,
		tiebreaker: "tensorflow",
	}
	ogGroup := &group{
		pkgs:       []*apk.Package{pkg1},
		size:       5000,
		tiebreaker: "py3-ml",
	}

	groups := []*group{ogGroup, tfGroup, numpyGroup}

	pkgToDiff := map[*apk.Package][]byte{
		pkg1: []byte("P:py3-ml\nV:1.0.0\n\n"),
	}

	layers, err := splitLayers(ctx, fsys, groups, pkgToDiff, t.TempDir())
	if err != nil {
		t.Fatalf("splitLayers: %v", err)
	}

	if len(layers) != 4 {
		t.Fatalf("got %d layers, want 4", len(layers))
	}

	for _, idx := range []int{1, 2} {
		rc, err := layers[idx].Uncompressed()
		if err != nil {
			t.Fatalf("layer %d Uncompressed: %v", idx, err)
		}

		tr := tar.NewReader(rc)
		foundIdb := false
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("layer %d tar read: %v", idx, err)
			}
			if hdr.Name == "usr/lib/apk/db/installed" {
				foundIdb = true
				content, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				if len(content) == 0 {
					t.Errorf("layer %d idb is empty", idx)
				}
			}
		}
		rc.Close()

		if !foundIdb {
			t.Errorf("package layer %d missing idb entry", idx)
		}
	}
}

func TestResolveGroups(t *testing.T) {
	fsys := apkfs.NewMemFS()

	if err := fsys.MkdirAll("usr/lib/python3.12/site-packages/numpy", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile("usr/lib/python3.12/site-packages/numpy/__init__.py", make([]byte, 500), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile("usr/lib/python3.12/site-packages/numpy/core.so", make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &group{
		paths: []string{"usr/lib/python3.12/site-packages/numpy"},
	}

	if err := resolveGroups(fsys, []*group{g}); err != nil {
		t.Fatalf("resolveGroups: %v", err)
	}

	if len(g.pkgs) != 0 {
		t.Errorf("expected empty pkgs with memFS, got %d", len(g.pkgs))
	}

	if g.size != 1500 {
		t.Errorf("group size = %d, want 1500", g.size)
	}
}


func assertContains(t *testing.T, paths []string, want string) {
	t.Helper()
	if !slices.Contains(paths, want) {
		t.Errorf("paths missing %q", want)
	}
}

func assertContainsPrefix(t *testing.T, paths []string, prefix string) {
	t.Helper()
	for _, p := range paths {
		if strings.HasPrefix(p, prefix) {
			return
		}
	}
	t.Errorf("paths missing any entry with prefix %q", prefix)
}
