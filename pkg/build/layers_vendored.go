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
	"fmt"
	"io/fs"
	"slices"

	"chainguard.dev/apko/pkg/apk/apk"
	apkfs "chainguard.dev/apko/pkg/apk/fs"
)

// Responsible for splitting packages vendored in an APK
type vendoredSplitter interface {
	Name() string
	Discover(fsys apkfs.FullFS) ([]*group, error)
}

var knownVendoredSplitters = []vendoredSplitter{
	&pythonSplitter{},
}

func discoverVendoredGroups(fsys apkfs.FullFS) ([]*group, error) {
	var all []*group
	for _, s := range knownVendoredSplitters {
		groups, err := s.Discover(fsys)
		if err != nil {
			return nil, fmt.Errorf("discovering %s packages: %w", s.Name(), err)
		}
		all = append(all, groups...)
	}
	return all, nil
}

func resolveGroups(fsys apkfs.FullFS, groups []*group) error {
	for _, g := range groups {
		seen := map[string]struct{}{}
		for _, p := range g.paths {
			err := fs.WalkDir(fsys, p, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return fmt.Errorf("walking %s: %w", path, err)
				}
				if d.IsDir() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return fmt.Errorf("stat %s: %w", path, err)
				}

				g.size += uint64(info.Size())

				pkger, ok := info.(interface {
					Package() *apk.Package
				})
				if !ok {
					return nil
				}
				pkg := pkger.Package()
				if pkg == nil {
					return fmt.Errorf("file %s has no owning package", path)
				}
				if _, dup := seen[pkg.Name]; !dup {
					seen[pkg.Name] = struct{}{}
					g.pkgs = append(g.pkgs, pkg)
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("resolving group for path %s: %w", p, err)
			}
		}

		slices.SortFunc(g.pkgs, func(a, b *apk.Package) int {
			return cmp.Compare(a.Name, b.Name)
		})
	}
	return nil
}

func groupByVendored(fsys apkfs.FullFS, budget int, distribution string) ([]*group, error) {
	groups, err := discoverVendoredGroups(fsys)
	if err != nil {
		return nil, err
	}

	if len(groups) == 0 {
		return nil, fmt.Errorf("vendored layering strategy selected, but no vendored packages found")
	}

	if err := resolveGroups(fsys, groups); err != nil {
		return nil, err
	}

	return assignGroups(groups, budget, distribution)
}
