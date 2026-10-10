// SPDX-License-Identifier: Apache-2.0

// Package supervisor runs an application's long-running components (HTTP
// servers, queue workers, pub/sub listeners, the scheduler and background
// tasks) as goroutines in one process, and shuts them down gracefully.
//
// A [Supervisor] provides:
//
//   - Process types: each component declares process types such as "web"
//     or "worker" (Heroku's word, from the Procfile), and [Supervisor.Run]
//     can start only some of them, so one binary can run everything on a
//     small server or be split across machines.
//   - Restart policies: a failing component can be left stopped
//     ([RestartNever]), restarted with exponential backoff
//     ([RestartOnFailure]), or stop the whole application ([StopOnFailure]).
//     Panics are recovered and treated as failures.
//   - Staged shutdown: on shutdown, stages are canceled in ascending order
//     ([StageIngress] first, [StageBackground] last), each draining before
//     the next is canceled, all within one timeout.
//   - Health: [Supervisor.Ready] and [Supervisor.Status] feed readiness
//     probes and diagnostics.
//
// Most applications use the supervisor through anetos.App (app.Go,
// app.Component and app.Run) rather than directly.
//
// Concept guide: docs/site/concepts/runtime-supervisor.md.
package supervisor
