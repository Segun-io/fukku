package flows

import "time"

// SetWriteTimeout shortens the limit of the writes that follow a change in an app.
func (m *Matcher) SetWriteTimeout(d time.Duration) { m.writeTimeout = d }

// SetWorkerDown replaces the worker heartbeat check of the flow 2 step.
func (p *Proposals) SetWorkerDown(f func() (string, bool)) { p.workerDown = f }
