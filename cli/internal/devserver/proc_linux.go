// SPDX-License-Identifier: Apache-2.0

package devserver

import "syscall"

// sysProcAttr makes the kernel stop the app if anetos dev dies (even from
// SIGKILL), so no orphan keeps the port.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
