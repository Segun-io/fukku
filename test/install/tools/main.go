// Command tools is the Go side of the clean-room install test (test/install/run.sh). It runs
// inside the clean host, in a container made from this one binary, and has four jobs:
//
//	authstub   a minimal forward-authentication service for the reverse proxy: it sets the
//	           user and group headers of a user that signed in (docs/reverse-proxy.md)
//	seed       test scaffolding: configure the test Radarr, Sonarr and Jellyfin, which in a
//	           real installation already exist. It is not one of the operator's steps.
//	check      what the install must achieve, as the e2e helpers drive it: readiness, sign-in
//	           through the proxy, a film (flow 1) and a season archive (flow 3) imported
//	ping       an HTTP health check for containers that have no other tool
//
// Every address is a service name of the clean host's own networks; nothing here talks to
// anything else (Radarr and Sonarr reach their metadata services themselves).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tools authstub | seed | check | ping <url>")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "authstub":
		err = authstub(os.Args[2:])
	case "seed":
		err = seed(os.Args[2:])
	case "check":
		err = check(os.Args[2:])
	case "ping":
		err = ping(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tools:", err)
		os.Exit(1)
	}
}
