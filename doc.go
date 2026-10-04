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

// Package stiggy deploys multi-agent architectures on NATS. It glues two
// libraries together: phero (github.com/henomis/phero) provides the agents,
// and packtrail (github.com/henomis/packtrail) provides durable, event-sourced
// orchestration on JetStream.
//
// A fleet is described once, either as a YAML file run by the stiggy binary
// or in Go through this package, and both front ends produce the same model.
// Flows are packtrail flows with a few agent-specific fields; agents run
// in-process inside packtrail workers.
//
// stiggy never talks to NATS directly. It only opens a connection and hands
// it to phero and packtrail constructors; every subject, stream and bucket
// belongs to those libraries.
package stiggy
