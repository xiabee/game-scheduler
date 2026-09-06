// Package version carries the build version, injected at link time:
//
//	go build -ldflags "-X github.com/xiabee/game-scheduler/internal/version.Version=v1.2.3"
//
// Unset builds report "dev".
package version

var Version = "dev"
