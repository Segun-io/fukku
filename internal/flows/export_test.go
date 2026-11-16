package flows

import "time"

// SetWriteTimeout shortens the limit of the writes that follow a change in an app.
func (m *Matcher) SetWriteTimeout(d time.Duration) { m.writeTimeout = d }
