//go:build !linux

package awg

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }
