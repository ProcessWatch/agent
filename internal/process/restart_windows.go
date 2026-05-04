//go:build windows

package process

import "syscall"

func detachAttr() *syscall.SysProcAttr {
	return nil
}
