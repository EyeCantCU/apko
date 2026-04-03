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
	"fmt"
	"strings"
	"testing"

	apkfs "chainguard.dev/apko/pkg/apk/fs"
)

func TestPythonPackageName(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"numpy", "numpy"},
		{"numpy-1.26.4.dist-info", "numpy"},
		{"google_cloud_storage", "google_cloud_storage"},
		{"google-cloud-storage-2.0.0.dist-info", "google_cloud_storage"},
		{"PIL", "pil"},
		{"Pillow-10.0.0.dist-info", "pillow"},
		{"Pillow-10.0.0.egg-info", "pillow"},
		{"requests", "requests"},
		{"urllib3", "urllib3"},
		{"foo.py", "foo"},
		{"bar.pth", "bar"},
		{"_distutils_hack", "_distutils_hack"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := pythonPackageName(tc.input); got != tc.want {
				t.Errorf("pythonPackageName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestStripDistInfoVersion(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"numpy-1.26.4", "numpy"},
		{"google-cloud-storage-2.0.0", "google-cloud-storage"},
		{"Pillow-10.0.0", "Pillow"},
		{"foo", "foo"},
		{"requests-2.31.0", "requests"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got := stripDistInfoVersion(tc.input)
			if got != tc.want {
				t.Errorf("stripDistInfoVersion(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// writeDistInfo creates a dist-info directory with a RECORD file, and also
// creates the files listed in the RECORD so that resolveGroups can walk them.
func writeDistInfo(t *testing.T, fsys apkfs.FullFS, spDir, distInfoName string, recordLines []string) {
	t.Helper()
	diDir := spDir + "/" + distInfoName
	if err := fsys.MkdirAll(diDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := strings.Join(recordLines, "\n") + "\n"
	if err := fsys.WriteFile(diDir+"/RECORD", []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create the actual files referenced in RECORD.
	for _, line := range recordLines {
		filePath, _, _ := strings.Cut(line, ",")
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			continue
		}
		fullPath := spDir + "/" + filePath
		dir, _, _ := strings.Cut(fullPath, "/")
		_ = dir
		if err := fsys.MkdirAll(fullPath[:strings.LastIndex(fullPath, "/")], 0o755); err != nil {
			t.Fatal(err)
		}
		if err := fsys.WriteFile(fullPath, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPythonSplitterDiscover(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	writeDistInfo(t, fsys, sp, "numpy-1.26.4.dist-info", []string{
		"numpy/__init__.py,sha256=abc,100",
		"numpy/core/multiarray.so,sha256=def,1000",
	})
	writeDistInfo(t, fsys, sp, "tensorflow-2.15.0.dist-info", []string{
		"tensorflow/__init__.py,sha256=abc,200",
		"tensorflow/python/ops.so,sha256=def,5000",
	})
	writeDistInfo(t, fsys, sp, "keras-3.0.0.dist-info", []string{
		"keras/__init__.py,sha256=abc,50",
	})

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(packages) != 3 {
		t.Fatalf("got %d packages, want 3; packages: %v", len(packages), groupNames(packages))
	}

	wantNames := []string{"keras", "numpy", "tensorflow"}
	for i, pkg := range packages {
		if pkg.tiebreaker != wantNames[i] {
			t.Errorf("package[%d].name = %q, want %q", i, pkg.tiebreaker, wantNames[i])
		}
	}
}

func TestPythonSplitterNoPython(t *testing.T) {
	fsys := apkfs.NewMemFS()
	if err := fsys.MkdirAll("usr/lib", 0o755); err != nil {
		t.Fatal(err)
	}

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("expected no packages, got %d", len(packages))
	}
}

func TestPythonSplitterNoUsrLib(t *testing.T) {
	fsys := apkfs.NewMemFS()

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("expected no packages, got %d", len(packages))
	}
}

func TestPythonSplitterUsrLocal(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/local/lib/python3.12/site-packages"

	writeDistInfo(t, fsys, sp, "requests-2.31.0.dist-info", []string{
		"requests/__init__.py,sha256=abc,500",
	})

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 {
		t.Fatalf("got %d packages, want 1", len(packages))
	}
	if packages[0].tiebreaker != "requests" {
		t.Errorf("package name = %q, want %q", packages[0].tiebreaker, "requests")
	}
}

func TestPythonSplitterVendoredApp(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "opt/myapp/lib/python3.12/site-packages"

	writeDistInfo(t, fsys, sp, "flask-3.0.0.dist-info", []string{
		"flask/__init__.py,sha256=abc,300",
	})

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 {
		t.Fatalf("got %d packages, want 1", len(packages))
	}
	if packages[0].tiebreaker != "flask" {
		t.Errorf("package name = %q, want %q", packages[0].tiebreaker, "flask")
	}
}

func TestPythonSplitterMergesAcrossSitePackages(t *testing.T) {
	fsys := apkfs.NewMemFS()

	for _, sp := range []string{
		"usr/lib/python3.12/site-packages",
		"usr/local/lib/python3.12/site-packages",
	} {
		writeDistInfo(t, fsys, sp, "numpy-1.26.4.dist-info", []string{
			"numpy/__init__.py,sha256=abc,100",
		})
	}

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}

	if len(packages) != 1 {
		t.Fatalf("got %d packages, want 1 (merged)", len(packages))
	}
	if packages[0].tiebreaker != "numpy" {
		t.Errorf("name = %q, want %q", packages[0].tiebreaker, "numpy")
	}
}

func TestMultiplePythonVersions(t *testing.T) {
	fsys := apkfs.NewMemFS()

	for _, ver := range []string{"python3.11", "python3.12"} {
		sp := fmt.Sprintf("usr/lib/%s/site-packages", ver)
		writeDistInfo(t, fsys, sp, "numpy-1.26.4.dist-info", []string{
			"numpy/__init__.py,sha256=abc,100",
		})
	}

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}

	if len(packages) != 1 {
		t.Fatalf("got %d packages, want 1 (merged across versions)", len(packages))
	}
	if packages[0].tiebreaker != "numpy" {
		t.Errorf("package name = %q, want %q", packages[0].tiebreaker, "numpy")
	}
}

func TestPythonSplitterNamespacePackage(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	writeDistInfo(t, fsys, sp, "nvidia_cuda_runtime_cu12-12.1.105.dist-info", []string{
		"nvidia/cuda_runtime/__init__.py,sha256=abc,0",
		"nvidia/cuda_runtime/lib.so,sha256=def,5000",
	})
	writeDistInfo(t, fsys, sp, "nvidia_cudnn_cu12-8.9.2.26.dist-info", []string{
		"nvidia/cudnn/__init__.py,sha256=abc,0",
		"nvidia/cudnn/lib.so,sha256=def,5000",
	})

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(packages) != 2 {
		t.Fatalf("got %d packages, want 2; packages: %v", len(packages), groupNames(packages))
	}

	for _, pkg := range packages {
		switch pkg.tiebreaker {
		case "nvidia_cuda_runtime_cu12":
			assertContainsPrefix(t, groupPaths(pkg), sp+"/nvidia/cuda_runtime/__init__.py")
			assertContainsPrefix(t, groupPaths(pkg), sp+"/nvidia/cuda_runtime/lib.so")
		case "nvidia_cudnn_cu12":
			assertContainsPrefix(t, groupPaths(pkg), sp+"/nvidia/cudnn/__init__.py")
			assertContainsPrefix(t, groupPaths(pkg), sp+"/nvidia/cudnn/lib.so")
		default:
			t.Errorf("unexpected package %q", pkg.tiebreaker)
		}
	}
}

func TestPythonSplitterNamespaceNoRECORD(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	// dist-info without a RECORD produces a package with no file paths.
	diDir := sp + "/nvidia_cuda_runtime_cu12-12.1.105.dist-info"
	if err := fsys.MkdirAll(diDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile(diDir+"/METADATA", []byte("meta"), 0o644); err != nil {
		t.Fatal(err)
	}

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(packages) != 1 {
		t.Fatalf("got %d packages, want 1; packages: %v", len(packages), groupNames(packages))
	}
	if packages[0].tiebreaker != "nvidia_cuda_runtime_cu12" {
		t.Errorf("name = %q, want %q", packages[0].tiebreaker, "nvidia_cuda_runtime_cu12")
	}
}

func TestPythonSplitterMixedNamespaceAndRegular(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	writeDistInfo(t, fsys, sp, "numpy-1.26.4.dist-info", []string{
		"numpy/__init__.py,sha256=abc,100",
	})
	writeDistInfo(t, fsys, sp, "nvidia_cuda_runtime_cu12-12.1.105.dist-info", []string{
		"nvidia/cuda_runtime/__init__.py,sha256=abc,0",
	})

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	names := groupNames(packages)
	assertContains(t, names, "numpy")
	assertContains(t, names, "nvidia_cuda_runtime_cu12")
}

func TestPythonSplitterLooseFileOwnership(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	writeDistInfo(t, fsys, sp, "setuptools-69.0.0.dist-info", []string{
		"setuptools/__init__.py,sha256=abc,100",
		"_distutils_hack/__init__.py,sha256=def,50",
		"pkg_resources/__init__.py,sha256=ghi,200",
	})

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// Everything should be under setuptools — no separate packages.
	if len(packages) != 1 {
		t.Fatalf("got %d packages, want 1; packages: %v", len(packages), groupNames(packages))
	}
	if packages[0].tiebreaker != "setuptools" {
		t.Errorf("name = %q, want %q", packages[0].tiebreaker, "setuptools")
	}
	assertContainsPrefix(t, groupPaths(packages[0]), sp+"/setuptools/__init__.py")
	assertContainsPrefix(t, groupPaths(packages[0]), sp+"/_distutils_hack/__init__.py")
	assertContainsPrefix(t, groupPaths(packages[0]), sp+"/pkg_resources/__init__.py")
}

func TestPythonSplitterDataDir(t *testing.T) {
	fsys := apkfs.NewMemFS()
	sp := "usr/lib/python3.12/site-packages"

	writeDistInfo(t, fsys, sp, "numpy-1.26.4.dist-info", []string{
		"numpy/__init__.py,sha256=abc,100",
		"numpy-1.26.4.data/scripts/f2py,sha256=def,500",
	})

	splitter := &pythonSplitter{}
	packages, err := splitter.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	for _, pkg := range packages {
		if pkg.tiebreaker != "numpy" {
			continue
		}
		assertContainsPrefix(t, groupPaths(pkg), sp+"/numpy/__init__.py")
		assertContainsPrefix(t, groupPaths(pkg), sp+"/numpy-1.26.4.data/scripts/f2py")
	}
}

func groupNames(groups []*group) []string {
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = g.tiebreaker
	}
	return names
}

func groupPaths(g *group) []string {
	return g.paths
}
