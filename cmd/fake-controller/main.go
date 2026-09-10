// fake-controller — a protocol-faithful stand-in for the Rust native
// controller (NC6 testing seam). It speaks the real wire protocol over
// real stdout pipes per --script, with no capture, no perception and no
// input: Windows smoke and Go integration tests use it to exercise the
// scheduler↔controller session lifecycle without a game or a controller
// build. It ignores any controller-style flags it does not know, so a
// native task's argument vector can be passed through unchanged.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	script := "happy"
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--script" && i+1 < len(os.Args) {
			script = os.Args[i+1]
			i++
		}
	}
	say := func(s string) { fmt.Fprintln(os.Stdout, s) }
	seq := 0
	next := func() int { seq++; return seq }

	switch script {
	case "happy":
		say(hello(next()))
		say(ready(next()))
		for c := 1; c <= 3; c++ {
			say(event(next(), c, fmt.Sprintf("step_%02d", c)))
		}
		say(result(next(), "done"))
	case "fail":
		say(hello(next()))
		say(result(next(), "failed"))
	case "hang":
		say(hello(next()))
		say(ready(next()))
		time.Sleep(60 * time.Second)
	case "noresult":
		say(hello(next()))
	case "badversion":
		say(`{"v":2,"seq":0,"ts":"t","type":"HELLO","payload":{"protocol_version":2,"controller_version":"future"}}`)
		time.Sleep(5 * time.Second)
	default:
		fmt.Fprintf(os.Stderr, "fake-controller: unknown script %q\n", script)
		os.Exit(2)
	}
}

func hello(seq int) string {
	return `{"v":1,"seq":` + strconv.Itoa(seq) + `,"ts":"t","type":"HELLO","payload":{"protocol_version":1,"controller_version":"fake-controller 0.1.0"}}`
}

func ready(seq int) string {
	return `{"v":1,"seq":` + strconv.Itoa(seq) + `,"ts":"t","type":"READY","payload":{"session_id":"fake-1","backend":"fake"}}`
}

func event(seq, cycle int, state string) string {
	return `{"v":1,"seq":` + strconv.Itoa(seq) + `,"ts":"t","type":"EVENT","payload":{"cycle":` +
		strconv.Itoa(cycle) + `,"state":"` + state + `","probes_fired":[],"detections":[],"planned_actions":[]}}`
}

func result(seq int, outcome string) string {
	return `{"v":1,"seq":` + strconv.Itoa(seq) + `,"ts":"t","type":"RESULT","payload":{"outcome":"` +
		outcome + `","state":"done","cycles":3,"inference_count":0,"cache_hits":0}}`
}
