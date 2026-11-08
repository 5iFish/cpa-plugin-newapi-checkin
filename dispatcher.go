//go:build !abiprobe

package main

import (
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/abi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/app"
)

func newDispatcher(host abi.HostCaller) abi.Dispatcher { return app.New(host) }
