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

// Package archguard enforces stiggy's central architecture rule: stiggy never
// touches NATS directly. It may open a connection and pass it to phero and
// packtrail constructors, and nothing else — no subjects, no JetStream, no
// KV, no micro services, no embedded server outside packtrailtest.
//
// The check is type-based (go/packages), so it sees through aliases, method
// values and interfaces returned by other libraries: any use of an object
// declared in nats.go that is not on the allowlist is a violation.
package archguard

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const natsPkg = "github.com/nats-io/nats.go"

// Receiver type names that have allowed methods.
const (
	connType   = "Conn"
	headerType = "Header"
	statusType = "Status"
)

// forbiddenImports are packages stiggy must never import. The embedded server
// is reachable through packtrail's public packtrailtest package instead.
var forbiddenImports = []string{
	natsPkg + "/jetstream",
	natsPkg + "/micro",
	"github.com/nats-io/nats-server/v2",
}

// allowedObjects are the package-level nats.go identifiers stiggy may use:
// the connection type, connection options and the header type phero's
// request API takes.
var allowedObjects = map[string]bool{
	connType: true, headerType: true, statusType: true, "Option": true, "DefaultURL": true,
	"Name": true, "MaxReconnects": true, "ReconnectWait": true, "RetryOnFailedConnect": true,
	"UserCredentials": true, "UserInfo": true, "Token": true, "Timeout": true,
	"RootCAs": true, "ClientCert": true, "Secure": true,
	"CONNECTED": true, "CONNECTING": true, "RECONNECTING": true, "DISCONNECTED": true,
	"CLOSED": true, "DRAINING_SUBS": true, "DRAINING_PUBS": true,
	"ErrConnectionClosed": true, "ErrNoServers": true,
}

// dialObject is allowed only in Rules.DialPackages.
const dialObject = "Connect"

// allowedMethods are the nats.go methods stiggy may call, by receiver type:
// connection lifecycle and introspection, and header manipulation.
var allowedMethods = map[string]map[string]bool{
	connType: {
		"Close": true, "Drain": true, "Status": true, "IsConnected": true, "IsClosed": true,
		"IsDraining": true, "IsReconnecting": true, "ConnectedUrl": true, "ConnectedServerName": true,
		"MaxPayload": true, "LastError": true,
	},
	headerType: {"Get": true, "Set": true, "Add": true, "Del": true, "Values": true},
	statusType: {"String": true},
}

// Rules configures a check.
type Rules struct {
	// DialPackages lists the import paths allowed to call nats.Connect.
	DialPackages []string
}

// Violation is one breach of the rule.
type Violation struct {
	Pos token.Position
	Msg string
}

func (v Violation) String() string { return fmt.Sprintf("%s: %s", v.Pos, v.Msg) }

// ErrLoad is returned when the packages to check cannot be loaded or do not
// type-check.
var ErrLoad = errors.New("archguard: load")

// Check loads patterns (test files included) relative to dir and returns
// every violation, sorted by position.
func Check(dir string, rules Rules, patterns ...string) ([]Violation, error) {
	cfg := &packages.Config{
		Dir:   dir,
		Tests: true,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax,
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLoad, err)
	}

	var loadErrs []error

	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e)
		}
	})

	if len(loadErrs) > 0 {
		return nil, fmt.Errorf("%w: %w", ErrLoad, errors.Join(loadErrs...))
	}

	seen := map[string]bool{}

	var out []Violation

	add := func(v Violation) {
		k := v.String()
		if seen[k] {
			return
		}

		seen[k] = true

		out = append(out, v)
	}

	for _, p := range pkgs {
		checkPackage(p, rules, add)
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Pos, out[j].Pos
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}

		return a.Offset < b.Offset
	})

	return out, nil
}

func checkPackage(p *packages.Package, rules Rules, add func(Violation)) {
	for _, f := range p.Syntax {
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbiddenImports {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					add(Violation{p.Fset.Position(imp.Pos()), "forbidden import " + path})
				}
			}
		}
	}

	// Test variants carry a "[pkg.test]" suffix in their ID but share the
	// import path, which is what DialPackages lists.
	canDial := slices.Contains(rules.DialPackages, strings.TrimSuffix(p.PkgPath, "_test"))

	for id, obj := range p.TypesInfo.Uses {
		if obj.Pkg() == nil || obj.Pkg().Path() != natsPkg {
			continue
		}

		if msg := judge(obj, canDial); msg != "" {
			add(Violation{p.Fset.Position(id.Pos()), msg})
		}
	}
}

// judge returns why obj may not be used, or "" when it may.
func judge(obj types.Object, canDial bool) string {
	switch o := obj.(type) {
	case *types.Func:
		sig, _ := o.Type().(*types.Signature)
		if sig != nil && sig.Recv() != nil {
			recv := receiverName(sig.Recv().Type())
			if allowedMethods[recv][o.Name()] {
				return ""
			}

			return fmt.Sprintf("nats.%s.%s: direct NATS access is not allowed", recv, o.Name())
		}
	case *types.Var:
		if o.IsField() {
			return "nats field " + o.Name() + ": direct NATS access is not allowed"
		}
	}

	name := obj.Name()

	switch {
	case name == dialObject && canDial:
		return ""
	case name == dialObject:
		return "nats.Connect: only internal/natsconn may dial NATS"
	case allowedObjects[name]:
		return ""
	default:
		return "nats." + name + ": direct NATS access is not allowed"
	}
}

func receiverName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}

	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}

	return t.String()
}
