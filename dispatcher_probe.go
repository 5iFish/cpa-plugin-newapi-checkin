//go:build abiprobe

package main

import (
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/abi"
	probe "github.com/5ifish/cpa-plugin-newapi-checkin/tests/integration/testdata/probe"
)

func newDispatcher(host abi.HostCaller) abi.Dispatcher { return probe.New(host) }
