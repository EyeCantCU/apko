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
	"cmp"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	apkfs "chainguard.dev/apko/pkg/apk/fs"
)

type pythonSplitter struct{}

func (p *pythonSplitter) Name() string { return "python" }

func (p *pythonSplitter) Discover(fsys apkfs.FullFS) ([]*group, error) {
	var spDirs []string
	_ = fs.WalkDir(fsys, ".", func(dir string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == "site-packages" {
			spDirs = append(spDirs, dir)
			return fs.SkipDir
		}
		return nil
	})

	gMap := map[string]*group{}
	ensureGroup := func(name string) *group {
		if g, ok := gMap[name]; ok {
			return g
		}
		g := &group{tiebreaker: name}
		gMap[name] = g
		return g
	}

	for _, spDir := range spDirs {
		spEntries, err := fsys.ReadDir(spDir)
		if err != nil {
			continue
		}

		for _, e := range spEntries {
			name := e.Name()
			if !e.IsDir() || !strings.HasSuffix(name, ".dist-info") {
				continue
			}
			g := ensureGroup(pythonPackageName(name))
			g.paths = append(g.paths, parsePaths(fsys, path.Join(spDir, name, "RECORD"), spDir)...)
		}
	}

	groups := make([]*group, 0, len(gMap))
	for _, g := range gMap {
		slices.Sort(g.paths)
		groups = append(groups, g)
	}

	slices.SortFunc(groups, func(a, b *group) int {
		return cmp.Compare(a.tiebreaker, b.tiebreaker)
	})

	return groups, nil
}

// parsePaths reads a RECORD file and returns the file paths it contains,
// resolved relative to the site-packages directory.
func parsePaths(fsys apkfs.FullFS, recordPath, spDir string) []string {
	data, err := fsys.ReadFile(recordPath)
	if err != nil {
		return nil
	}
	var paths []string
	for line := range strings.SplitSeq(string(data), "\n") {
		filePath, _, _ := strings.Cut(line, ",")
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			continue
		}
		paths = append(paths, path.Join(spDir, filePath))
	}
	return paths
}

var distInfoVersion = regexp.MustCompile(`-\d[^-]*$`)
var pep503Replacer = strings.NewReplacer("-", "_", ".", "_")

func normalizePythonName(name string) string {
	return pep503Replacer.Replace(strings.ToLower(name))
}

func stripDistInfoVersion(name string) string {
	return distInfoVersion.ReplaceAllString(name, "")
}

func pythonPackageName(name string) string {
	return normalizePythonName(stripDistInfoVersion(strings.TrimSuffix(name, path.Ext(name))))
}
