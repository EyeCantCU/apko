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
	"slices"

	"chainguard.dev/apko/pkg/apk/apk"
)

func replacesGroup(rep string, g *group) (bool, error) {
	constraint := apk.ResolvePackageNameVersionPin(rep)

	// Look for the package to make sure the version satisfies Replaces.
	for _, pkg := range g.pkgs {
		if pkg.Name != constraint.Name {
			// This is not the package we're looking for.
			continue
		}

		ver, err := apk.ParseVersion(pkg.Version)
		if err != nil {
			return false, fmt.Errorf("parsing %s version %s: %w", pkg.Name, pkg.Version, err)
		}

		ok, err := constraint.SatisfiedBy(ver)
		if err != nil {
			return false, fmt.Errorf("checking %s satisfies %s: %w", pkg.Version, constraint.Name, err)
		}

		if ok {
			return true, nil
		}
	}

	return false, nil
}

func groupByOriginAndSize(pkgs []*apk.Package, budget int, distribution string) ([]*group, error) {
	// First, we're going to group packages by their origin.
	byOrigin := map[string]*group{}
	for _, pkg := range pkgs {
		origin := pkg.Origin
		g, ok := byOrigin[origin]
		if !ok {
			g = &group{}
			byOrigin[origin] = g
		}

		g.pkgs = append(g.pkgs, pkg)
	}

	// Then we need to merge any packages that replace each other.
	byPackage := map[string]*group{}
	for _, g := range byOrigin {
		for _, pkg := range g.pkgs {
			byPackage[pkg.Name] = g
		}
	}

	replaceMap := map[string][]string{}
	for _, g := range byPackage {
		for _, pkg := range g.pkgs {
			if len(pkg.Replaces) == 0 {
				continue
			}

			replaceMap[pkg.Name] = pkg.Replaces
		}
	}

	for pkg, replaces := range replaceMap {
		for _, rep := range replaces {
			constraint := apk.ResolvePackageNameVersionPin(rep)

			replacee, ok := byPackage[constraint.Name]
			if !ok {
				// Whatever this package replaces is not in the image, that's normal.
				continue
			}

			if ok, err := replacesGroup(rep, replacee); err != nil {
				return nil, fmt.Errorf("checking %s replaces %s: %w", pkg, constraint.Name, err)
			} else if !ok {
				continue
			}

			g, ok := byPackage[pkg]
			if !ok {
				panic(fmt.Errorf("byPackage[%q] missing", pkg))
			}

			// If they're already merged, nothing to do.
			if replacee == g {
				continue
			}

			// Otherwise, we need to merge the two groups.
			merged := merge(g, replacee)

			// Update our maps so we can test identity above.
			for _, pkg := range merged.pkgs {
				byPackage[pkg.Name] = merged
				byOrigin[pkg.Origin] = merged
			}
		}
	}

	// Now we need to pick the best groups to keep.
	// First pass we'll set the size of each group to the sum of the installed size of all its packages.
	groups := make([]*group, 0, budget)
	seen := map[*group]struct{}{}
	for _, v := range byOrigin {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		groups = append(groups, v)
	}
	for _, g := range groups {
		for _, pkg := range g.pkgs {
			g.size += pkg.InstalledSize
			g.tiebreaker = max(g.tiebreaker, pkg.Name)
		}
	}

	result, err := assignGroups(groups, budget, distribution)
	if err != nil {
		return nil, err
	}

	// Sort packages so they're in a consistent order.
	for _, g := range result {
		slices.SortFunc(g.pkgs, func(a, b *apk.Package) int {
			return cmp.Compare(a.Name, b.Name)
		})
	}

	return result, nil
}
