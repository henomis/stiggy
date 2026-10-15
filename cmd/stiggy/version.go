// Copyright 2026 Simone Vellei
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

package main

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

const (
	develVersion = "(devel)"
	// shortRevision is how many characters of the commit hash are shown.
	shortRevision = 12
)

// version is set at build time with -ldflags "-X main.version=..."; when it
// is empty, the module version from the build info is used (go install).
var version string

// buildVersion reports the stiggy version and the phero and packtrail
// versions it was built with.
func buildVersion() (stiggy, phero, packtrail string) {
	stiggy, phero, packtrail = version, develVersion, develVersion

	info, ok := debug.ReadBuildInfo()
	if !ok {
		if stiggy == "" {
			stiggy = develVersion
		}

		return stiggy, phero, packtrail
	}

	if stiggy == "" {
		stiggy = info.Main.Version
		if stiggy == "" || stiggy == develVersion {
			stiggy = develVersion + revision(info)
		}
	}

	for _, dep := range info.Deps {
		switch dep.Path {
		case "github.com/henomis/phero/v2":
			phero = dep.Version
		case "github.com/henomis/packtrail":
			packtrail = dep.Version
		}
	}

	return stiggy, phero, packtrail
}

// revision is " <commit>[-dirty]" from the VCS build settings, or "".
func revision(info *debug.BuildInfo) string {
	var rev, dirty string

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}

	if rev == "" {
		return ""
	}

	if len(rev) > shortRevision {
		rev = rev[:shortRevision]
	}

	return " " + rev + dirty
}

func cmdVersion(stdout io.Writer) error {
	stiggy, phero, packtrail := buildVersion()

	_, _ = fmt.Fprintf(stdout, "stiggy %s\nphero %s\npacktrail %s\n%s %s/%s\n",
		stiggy, phero, packtrail, runtime.Version(), runtime.GOOS, runtime.GOARCH)

	return nil
}
