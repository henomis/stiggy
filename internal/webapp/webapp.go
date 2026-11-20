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

// Package webapp is what stiggy's browser tools share: the page they serve
// (embedded, or a directory given with -ui for development), the HTTP server
// with its graceful stop, and their version line.
package webapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

const (
	readHeaderTimeout = 10 * time.Second
	stopTimeout       = 5 * time.Second
	develVersion      = "(devel)"
	shortRevision     = 12
)

// Page is the page to serve: dir when set (a fresh build, for development),
// else the embedded one. The embedded files live under root in embedded.
func Page(embedded fs.FS, root, dir string) (fs.FS, error) {
	if dir != "" {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("-ui %s: not a directory", dir)
		}

		return os.DirFS(dir), nil
	}

	return fs.Sub(embedded, root)
}

// Serve serves h on addr until ctx ends, then stops gracefully. It prints
// "<name>: http://<address>" to stdout once listening.
//
// There is no write timeout: the pages hold long-lived event streams.
func Serve(ctx context.Context, name, addr string, h http.Handler, stdout io.Writer) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("-addr: %w", err)
	}

	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	_, _ = fmt.Fprintf(stdout, "%s: http://%s\n", name, ln.Addr())

	go func() {
		<-ctx.Done()

		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
		defer cancel()

		_ = srv.Shutdown(sctx)
	}()

	if err = srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s: %w", name, err)
	}

	return nil
}

// Version writes "<name> <version>" and the Go toolchain. version is the
// one set at build time (-ldflags -X); when empty, the module version from
// the build info is used (go install), or the commit for a local build.
func Version(w io.Writer, name, version string) {
	if version == "" {
		version = develVersion

		if info, ok := debug.ReadBuildInfo(); ok {
			if v := info.Main.Version; v != "" && v != develVersion {
				version = v
			} else {
				version += revision(info)
			}
		}
	}

	_, _ = fmt.Fprintf(w, "%s %s\n%s %s/%s\n", name, version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
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

	return " " + rev[:min(len(rev), shortRevision)] + dirty
}

// SplitList splits a comma-separated flag value, dropping empty entries.
func SplitList(s string) []string {
	var out []string

	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}
